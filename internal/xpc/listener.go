package xpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jen20/go-container/internal/mach"
)

// Listener is an anonymous XPC listener, equivalent to
// xpc_connection_create(NULL, NULL). Peers connect to it through its Endpoint.
type Listener struct {
	port mach.Port
	once sync.Once
}

// Listen creates an anonymous listener.
func Listen() (*Listener, error) {
	p, err := mach.AllocateReceive()
	if err != nil {
		return nil, err
	}
	if err := mach.InsertSendRight(p); err != nil {
		return nil, joinErrors(err, mach.DestroyReceive(p))
	}
	return &Listener{port: p}, nil
}

// Endpoint returns an endpoint for the listener. The endpoint is valid while
// the listener is open. Put it in a message to let a peer connect.
func (l *Listener) Endpoint() Endpoint { return Endpoint{Port: l.port} }

// Accept waits for a peer to connect. A zero timeout waits forever.
func (l *Listener) Accept(timeout time.Duration) (*PeerConn, error) {
	return l.accept(func() (*rawMessage, error) { return receiveRaw(l.port, timeout) })
}

// AcceptContext waits for a peer to connect until ctx is done. If ctx ends
// first, it returns ctx.Err(). Do not call Close while it waits. Cancel ctx,
// and close the listener after AcceptContext returns.
func (l *Listener) AcceptContext(ctx context.Context) (*PeerConn, error) {
	return l.accept(func() (*rawMessage, error) { return receiveRawContext(ctx, l.port) })
}

func (l *Listener) accept(recv func() (*rawMessage, error)) (*PeerConn, error) {
	for {
		m, err := recv()
		if err != nil {
			return nil, err
		}
		if m.header.ID != msgIDHandshake || len(m.ports) != 2 {
			// Not a connection request. libxpc drops these too.
			if err := m.release(); err != nil {
				return nil, err
			}
			continue
		}
		// Learn when the peer drops its send right, which is how a peer
		// closes the connection.
		if err := mach.RequestNoSenders(m.ports[0]); err != nil {
			return nil, joinErrors(err, m.release())
		}
		return &PeerConn{in: m.ports[0], out: m.ports[1]}, nil
	}
}

// Close stops the listener. Connected peers are not affected.
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		err = joinErrors(mach.Deallocate(l.port), mach.DestroyReceive(l.port))
	})
	return err
}

// PeerConn is the server side of one connection to a Listener.
type PeerConn struct {
	in   mach.Port // receive right: the peer sends to it
	out  mach.Port // send right: we send to the peer
	once sync.Once
}

// Message is a message that a PeerConn received.
type Message struct {
	Dictionary
	reply mach.Port // send-once right, or zero
}

// WantsReply reports whether the sender waits for a reply.
func (m *Message) WantsReply() bool { return m.reply != 0 }

// Receive waits for the next message from the peer. A zero timeout waits
// forever. It returns ErrClosed after the peer closes the connection.
func (p *PeerConn) Receive(timeout time.Duration) (*Message, error) {
	return p.receive(func() (*rawMessage, error) { return receiveRaw(p.in, timeout) })
}

// ReceiveContext is like Receive, but it waits until ctx is done. If ctx
// ends first, it returns ctx.Err(). Do not call Close while it waits.
func (p *PeerConn) ReceiveContext(ctx context.Context) (*Message, error) {
	return p.receive(func() (*rawMessage, error) { return receiveRawContext(ctx, p.in) })
}

func (p *PeerConn) receive(recv func() (*rawMessage, error)) (*Message, error) {
	for {
		m, err := recv()
		if err != nil {
			return nil, err
		}
		switch m.header.ID {
		case msgIDMessage, msgIDRequest:
		default:
			// The kernel sends a no-senders or dead-name notification
			// when the peer goes away.
			if err := joinErrors(releaseRemote(m.header), m.release()); err != nil {
				return nil, err
			}
			if m.header.ID == mach.NotifyNoSenders {
				return nil, ErrClosed
			}
			continue
		}
		d, err := decodeMessage(m.body, m.ports)
		if err != nil {
			return nil, joinErrors(err, releaseRemote(m.header))
		}
		msg := &Message{Dictionary: d}
		if m.header.ID == msgIDRequest {
			msg.reply = m.header.RemotePort
		} else if err := releaseRemote(m.header); err != nil {
			return nil, joinErrors(err, msg.Close())
		}
		return msg, nil
	}
}

// Close releases the message's file descriptors and ports. If the message
// wants a reply and none was sent, the sender gets ErrPeerGone.
func (m *Message) Close() error {
	var err error
	if m.reply != 0 {
		err = mach.Deallocate(m.reply)
		m.reply = 0
	}
	return joinErrors(err, m.Dictionary.Close())
}

// Reply answers a message that wants a reply. It can be called once.
func (p *PeerConn) Reply(to *Message, d Dictionary) error {
	if to.reply == 0 {
		return fmt.Errorf("xpc: message does not want a reply")
	}
	port := to.reply
	to.reply = 0
	return sendMessage(port, mach.MsgTypeMoveSendOnce, 0, msgIDReply, d, machSendOptions{})
}

// Send sends a one-way message to the peer.
func (p *PeerConn) Send(d Dictionary) error {
	return sendMessage(p.out, mach.MsgTypeCopySend, 0, msgIDMessage, d, machSendOptions{})
}

// Close ends the connection.
func (p *PeerConn) Close() error {
	var err error
	p.once.Do(func() {
		err = joinErrors(mach.DestroyReceive(p.in), mach.Deallocate(p.out))
	})
	return err
}

// receiveRawContext waits for one message on port until ctx is done. If ctx
// ends first, it returns ctx.Err().
func receiveRawContext(ctx context.Context, port mach.Port) (*rawMessage, error) {
	// When ctx ends, a msgIDCancel message wakes the receive. If a real
	// message wins the race, the cancel message stays queued, and a later
	// call whose ctx is still live skips it.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var mu sync.Mutex
		returned := false
		stop := context.AfterFunc(ctx, func() {
			mu.Lock()
			defer mu.Unlock()
			if !returned {
				wake(port)
			}
		})
		m, err := receiveRaw(port, 0)
		mu.Lock()
		returned = true
		mu.Unlock()
		stop()
		if err != nil {
			return nil, err
		}
		if m.header.ID != msgIDCancel {
			return m, nil
		}
		if err := m.release(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A stale cancel message from an earlier call. Wait again.
	}
}
