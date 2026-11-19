package xpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jen20/go-container/internal/mach"
)

// Mach message IDs that libxpc uses.
const (
	msgIDHandshake = 0x77303074 // "w00t": sets up a peer connection
	msgIDMessage   = 0x10000000 // one-way message
	msgIDRequest   = 0x40000000 // message that expects a reply

	// msgIDCancel wakes a RequestContext call that waits for a reply. Only
	// this process sends it, to its own reply port. It is not part of the
	// XPC protocol.
	msgIDCancel = 0x63616e63 // "canc"
)

const (
	portDescSize = 12
	oolDescSize  = 16
	// maxInlineMessage is the largest message that is sent with an inline
	// body. libxpc switches to an out-of-line body just below one page.
	maxInlineMessage = mach.PageSize - 128
	// receiveBufferSize is the first receive buffer size. A larger message
	// makes the receive retry with the size that the kernel reports.
	receiveBufferSize = 64 * 1024
	trailerAllowance  = 128
)

// ErrClosed is returned after Close.
var ErrClosed = errors.New("xpc: connection closed")

// ErrUnknownService is returned by Dial when no process has registered the
// Mach service, for example because the service is not running.
var ErrUnknownService = errors.New("xpc: service is not registered")

// ErrPeerGone is returned when the peer ends without replying.
var ErrPeerGone = errors.New("xpc: peer went away without replying")

// TimeoutError is returned when a reply does not arrive in time.
type TimeoutError struct{ Timeout time.Duration }

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("xpc: no reply within %s", e.Timeout)
}

// Conn is a client connection to a Mach service, equivalent to
// xpc_connection_create_mach_service with no flags.
type Conn struct {
	mu      sync.Mutex
	service string
	send    mach.Port // our send right; the peer holds the receive right
	recv    mach.Port // our receive right; the peer holds a send right
	closed  bool
}

// Dial looks up a Mach service in the bootstrap namespace and does the XPC
// connection handshake with it.
func Dial(service string) (*Conn, error) {
	return DialContext(context.Background(), service)
}

// DialContext looks up the service and sends the handshake. ctx bounds the
// Mach send if the service's queue is full.
func DialContext(ctx context.Context, service string) (*Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	svc, err := mach.LookUp(service)
	if err != nil {
		if errors.Is(err, mach.Error(mach.BootstrapUnknown)) {
			return nil, fmt.Errorf("xpc: look up %q: %w", service, ErrUnknownService)
		}
		return nil, fmt.Errorf("xpc: look up %q: %w", service, err)
	}
	c, err := dialPort(ctx, service, svc)
	if derr := mach.Deallocate(svc); derr != nil {
		derr = fmt.Errorf("xpc: release service port: %w", derr)
		if c != nil {
			return nil, joinErrors(derr, c.Close())
		}
		return nil, joinErrors(err, derr)
	}
	return c, err
}

// DialEndpoint connects to the listener behind an endpoint.
func DialEndpoint(ep Endpoint) (*Conn, error) {
	return dialPort(context.Background(), "endpoint", ep.Port)
}

func dialPort(ctx context.Context, label string, svc mach.Port) (*Conn, error) {
	var c *Conn
	err := sendMach(ctx, func(opts machSendOptions) error {
		var err error
		c, err = handshake(label, svc, opts)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("xpc: handshake with %s: %w", label, err)
	}
	return c, nil
}

func handshake(label string, svc mach.Port, opts machSendOptions) (*Conn, error) {
	send, err := mach.AllocateReceive()
	if err != nil {
		return nil, err
	}
	if err := mach.InsertSendRight(send); err != nil {
		return nil, joinErrors(err, mach.DestroyReceive(send))
	}
	recv, err := mach.AllocateReceive()
	if err != nil {
		return nil, joinErrors(err, mach.DestroyReceive(send), mach.Deallocate(send))
	}
	// The handshake moves the receive right of the send port to the peer and
	// gives it a send right to our receive port.
	msg := buildMessage(mach.Header{
		Bits:       mach.MsgBits(mach.MsgTypeCopySend, 0) | mach.MsgBitsComplex,
		RemotePort: svc,
		ID:         msgIDHandshake,
	}, []portDesc{
		{send, mach.MsgTypeMoveReceive},
		{recv, mach.MsgTypeMakeSend},
	}, nil, false)
	if err := opts.send(msg, func() error { return mach.DestroyReceive(send) }); err != nil {
		return nil, joinErrors(err, mach.Deallocate(send), mach.DestroyReceive(recv))
	}
	return &Conn{service: label, send: send, recv: recv}, nil
}

// buildMessage lays out a Mach message: header, port descriptors, and body.
// When oolBody is true, the body goes in an out-of-line descriptor after the
// port descriptors instead of inline. The caller must keep body alive until
// the message is sent.
func buildMessage(h mach.Header, ports []portDesc, body []byte, oolBody bool) []byte {
	ndesc := len(ports)
	if oolBody {
		ndesc++
	}
	size := mach.HeaderSize
	if ndesc > 0 {
		h.Bits |= mach.MsgBitsComplex
		size += 4 + len(ports)*portDescSize
		if oolBody {
			size += oolDescSize
		}
	}
	if !oolBody {
		size += len(body)
	}
	h.Size = uint32(size)
	msg := make([]byte, size)
	h.Put(msg)
	off := mach.HeaderSize
	if ndesc > 0 {
		le.PutUint32(msg[off:], uint32(ndesc))
		off += 4
		for _, p := range ports {
			le.PutUint32(msg[off:], uint32(p.name))
			msg[off+10] = p.disposition
			msg[off+11] = mach.DescPort
			off += portDescSize
		}
		if oolBody {
			le.PutUint64(msg[off:], uint64(bodyAddr(body)))
			msg[off+8] = 0 // deallocate: no, Go owns the memory
			msg[off+9] = 1 // MACH_MSG_VIRTUAL_COPY
			msg[off+11] = mach.DescOOL
			le.PutUint32(msg[off+12:], uint32(len(body)))
			off += oolDescSize
		}
	}
	if !oolBody {
		copy(msg[off:], body)
	}
	return msg
}

// Send sends a message that does not expect a reply.
func (c *Conn) Send(d Dictionary) error {
	return c.sendError(c.send1(d, 0, msgIDMessage, machSendOptions{}))
}

func (c *Conn) send1(d Dictionary, replyPort mach.Port, id int32, opts machSendOptions) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return sendMessage(c.send, mach.MsgTypeCopySend, replyPort, id, d, opts)
}

func (c *Conn) sendError(err error) error {
	if err == nil || err == ErrClosed {
		return err
	}
	return fmt.Errorf("xpc: send to %s: %w", c.service, err)
}

// sendMessage encodes d and sends it to dest. If reply is not zero, the
// message carries a send-once right to it.
func sendMessage(dest mach.Port, destDisposition uint32, reply mach.Port, id int32, d Dictionary, opts machSendOptions) error {
	releaseDest := func() error {
		if destDisposition == mach.MsgTypeMoveSendOnce {
			return mach.Deallocate(dest)
		}
		return nil
	}
	body, ports, err := encodeMessage(d)
	if err != nil {
		return joinErrors(err, releaseDest())
	}
	h := mach.Header{
		Bits:       mach.MsgBits(destDisposition, 0),
		RemotePort: dest,
		ID:         id,
	}
	if reply != 0 {
		h.Bits = mach.MsgBits(destDisposition, mach.MsgTypeMakeSendOnce)
		h.LocalPort = reply
	}
	inlineSize := mach.HeaderSize + len(body)
	if len(ports) > 0 {
		inlineSize += 4 + len(ports)*portDescSize
	}
	ool := inlineSize > maxInlineMessage
	msg := buildMessage(h, ports, body, ool)
	err = opts.send(msg, func() error { return joinErrors(releasePorts(ports), releaseDest()) })
	keepAlive(body)
	return err
}

type machSendOptions struct {
	flags   int32
	timeout uint32
}

func (o machSendOptions) send(msg []byte, discard func() error) error {
	err := mach.Msg(msg, mach.SendMsg|o.flags, uint32(len(msg)), 0, o.timeout)
	if err == nil {
		return nil
	}
	if sendReturned(err) {
		return joinErrors(err, mach.DestroyUnsent(msg))
	}
	return joinErrors(err, discard())
}

// sendMach retries queue timeouts and interruptions while ctx is live.
// Each attempt builds a fresh message, since the returned message can hold
// a send-once right for a reply port that cannot be reused for sending.
func sendMach(ctx context.Context, attempt func(machSendOptions) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		opts := machSendOptions{flags: mach.SendInterrupt}
		if ctx.Done() != nil {
			const interval = 50 * time.Millisecond
			timeout := interval
			if deadline, ok := ctx.Deadline(); ok {
				timeout = min(timeout, time.Until(deadline))
			}
			opts.flags |= mach.SendTimeout
			opts.timeout = uint32(max(timeout.Milliseconds(), 1))
		}
		err := attempt(opts)
		if err != mach.Error(mach.SendTimedOut) && err != mach.Error(mach.SendInterrupted) {
			return err
		}
	}
}

func sendReturned(err error) bool {
	me, ok := err.(mach.Error)
	return ok && (me&^mach.MsgErrorMask == mach.SendTimedOut || me&^mach.MsgErrorMask == mach.SendInterrupted)
}

// Request sends a message and waits for its reply. A zero timeout waits
// forever.
func (c *Conn) Request(d Dictionary, timeout time.Duration) (Dictionary, error) {
	reply, err := mach.AllocateReceive()
	if err != nil {
		return nil, err
	}
	var res Dictionary
	err = c.sendError(c.send1(d, reply, msgIDRequest, machSendOptions{}))
	if err == nil {
		res, err = receive(reply, timeout)
	}
	if derr := mach.DestroyReceive(reply); derr != nil {
		return nil, joinErrors(err, derr, res.Close())
	}
	return res, err
}

// errCancelled is returned by receive for a msgIDCancel message.
var errCancelled = errors.New("xpc: request cancelled")

// RequestContext sends a message and waits for its reply until ctx is done.
// If ctx ends first, it returns ctx.Err(). The server can still act on the
// message, and the kernel discards its reply.
func (c *Conn) RequestContext(ctx context.Context, d Dictionary) (Dictionary, error) {
	call, err := c.Go(ctx, d)
	if err != nil {
		return nil, err
	}
	return call.Wait()
}

// Call is a request that was sent and whose reply has not been received.
type Call struct {
	ctx      context.Context
	reply    mach.Port
	mu       sync.Mutex // orders the cancel message against the port release
	released bool
	stop     func() bool
	once     sync.Once
	result   Dictionary
	err      error
}

// Go sends a message that expects a reply and returns without waiting. When
// Go returns, the message is in the peer's queue, so a later message on the
// same connection arrives after it. Call Wait to get the reply. If ctx ends
// first, Wait returns ctx.Err().
func (c *Conn) Go(ctx context.Context, d Dictionary) (*Call, error) {
	var reply mach.Port
	err := sendMach(ctx, func(opts machSendOptions) error {
		var err error
		reply, err = mach.AllocateReceive()
		if err != nil {
			return err
		}
		if err := c.send1(d, reply, msgIDRequest, opts); err != nil {
			return joinErrors(err, mach.DestroyReceive(reply))
		}
		return nil
	})
	if err != nil {
		if err == ctx.Err() {
			return nil, err
		}
		return nil, c.sendError(err)
	}
	call := &Call{ctx: ctx, reply: reply}
	call.stop = context.AfterFunc(ctx, func() {
		call.mu.Lock()
		defer call.mu.Unlock()
		if !call.released {
			wake(call.reply)
		}
	})
	return call, nil
}

// release frees the reply port. After it, no cancel message is sent, so a
// freed port name that is reused never gets one.
func (call *Call) release() error {
	call.mu.Lock()
	defer call.mu.Unlock()
	call.released = true
	return mach.DestroyReceive(call.reply)
}

// Wait waits for the reply. It returns the same result if called again.
func (call *Call) Wait() (Dictionary, error) {
	call.once.Do(func() {
		defer call.stop()
		res, err := receive(call.reply, 0)
		if errors.Is(err, errCancelled) {
			err = call.ctx.Err()
		}
		if rerr := call.release(); rerr != nil {
			res, err = nil, joinErrors(err, rerr, res.Close())
		}
		call.result, call.err = res, err
	})
	return call.result, call.err
}

// wake sends an empty msgIDCancel message to a port that this process holds
// the receive right for. It does not wait if the queue is full, because a
// full queue wakes the receiver anyway.
func wake(port mach.Port) {
	buf := make([]byte, mach.HeaderSize)
	mach.Header{
		Bits:       mach.MsgBits(mach.MsgTypeMakeSend, 0),
		Size:       mach.HeaderSize,
		RemotePort: port,
		ID:         msgIDCancel,
	}.Put(buf)
	if err := mach.Msg(buf, mach.SendMsg|mach.SendTimeout, mach.HeaderSize, 0, 0); sendReturned(err) {
		_ = mach.DestroyUnsent(buf)
	}
}

// receive waits for one reply on port and decodes it.
func receive(port mach.Port, timeout time.Duration) (Dictionary, error) {
	m, err := receiveRaw(port, timeout)
	if err != nil {
		return nil, err
	}
	d, err := decodeReply(m)
	if rerr := releaseRemote(m.header); rerr != nil {
		return nil, joinErrors(err, rerr, d.Close())
	}
	return d, err
}

func decodeReply(m *rawMessage) (Dictionary, error) {
	switch m.header.ID {
	case msgIDCancel:
		return nil, errCancelled
	case mach.NotifySendOnce:
		// The peer destroyed our reply right without answering, which
		// happens when it exits or drops the connection.
		return nil, ErrPeerGone
	case msgIDReply:
		return decodeMessage(m.body, m.ports)
	}
	return nil, joinErrors(fmt.Errorf("xpc: expected a reply, got message id 0x%08x", uint32(m.header.ID)), m.release())
}

// releaseRemote releases the right in the remote port field of a received
// message header, if there is one.
func releaseRemote(h mach.Header) error {
	if h.RemotePort == 0 {
		return nil
	}
	return mach.Deallocate(h.RemotePort)
}

// rawMessage is a received Mach message split into its parts. The caller
// owns the ports.
type rawMessage struct {
	header mach.Header
	body   []byte
	ports  []mach.Port
}

func (m *rawMessage) release() error {
	var errs []error
	for _, p := range m.ports {
		errs = append(errs, mach.Deallocate(p))
	}
	return joinErrors(errs...)
}

// receiveRaw waits for one Mach message on port. A zero timeout waits
// forever.
func receiveRaw(port mach.Port, timeout time.Duration) (*rawMessage, error) {
	opts := int32(mach.RcvMsg | mach.RcvLarge)
	var ms uint32
	if timeout > 0 {
		opts |= mach.RcvTimeout
		ms = uint32(max(timeout.Milliseconds(), 1))
	}
	size := receiveBufferSize
	for {
		buf := make([]byte, size+trailerAllowance)
		err := mach.Msg(buf, opts, 0, port, ms)
		if err == nil {
			return splitMessage(buf)
		}
		var me mach.Error
		if errors.As(err, &me) {
			switch me {
			case mach.RcvTooLarge:
				// The kernel left the message queued and wrote its size.
				size = int(mach.ParseHeader(buf).Size)
				continue
			case mach.RcvTimedOut:
				return nil, &TimeoutError{Timeout: timeout}
			}
		}
		return nil, fmt.Errorf("xpc: receive: %w", err)
	}
}

func splitMessage(buf []byte) (*rawMessage, error) {
	m := &rawMessage{header: mach.ParseHeader(buf)}
	h := m.header
	off := mach.HeaderSize
	var oolAddr, oolSize uintptr
	if h.Bits&mach.MsgBitsComplex != 0 {
		n := int(le.Uint32(buf[off:]))
		off += 4
		for range n {
			switch buf[off+11] {
			case mach.DescPort:
				m.ports = append(m.ports, mach.Port(le.Uint32(buf[off:])))
				off += portDescSize
			case mach.DescOOL, mach.DescOOLVolatile:
				oolAddr = uintptr(le.Uint64(buf[off:]))
				oolSize = uintptr(le.Uint32(buf[off+12:]))
				off += oolDescSize
			default:
				return nil, joinErrors(fmt.Errorf("xpc: unexpected descriptor type %d", buf[off+11]), m.release())
			}
		}
	}
	if oolAddr != 0 {
		// A failed deallocation leaks the mapping but leaves the body
		// intact, so the message is still delivered.
		m.body, _ = mach.TakeOOL(oolAddr, int(oolSize))
	} else {
		m.body = buf[off:h.Size]
	}
	if debug {
		fmt.Fprintf(os.Stderr, "xpc: received id=0x%08x bits=0x%08x size=%d ports=%d ool=%d\n%s", uint32(h.ID), h.Bits, h.Size, len(m.ports), oolSize, hex.Dump(m.body))
	}
	return m, nil
}

// Close ends the connection. The peer sees the connection as invalid.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return joinErrors(mach.Deallocate(c.send), mach.DestroyReceive(c.recv))
}

var debug = os.Getenv("XPC_DEBUG") != ""
