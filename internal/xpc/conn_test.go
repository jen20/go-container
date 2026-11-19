package xpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/mach"
)

// loopback returns a client connection and the peer connection that a
// local listener accepted for it.
func loopback(t *testing.T) (*Conn, *PeerConn) {
	t.Helper()
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, l) })
	c, err := DialEndpoint(l.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, c) })
	p, err := l.Accept(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, p) })
	return c, p
}

// roundTrip sends d over a loopback connection and returns what the peer
// received.
func roundTrip(t *testing.T, d Dictionary) *Message {
	t.Helper()
	c, p := loopback(t)
	if err := c.Send(d); err != nil {
		t.Fatal(err)
	}
	m, err := p.Receive(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, m) })
	return m
}

func TestLoopbackTypes(t *testing.T) {
	sent := Dictionary{
		"s":    "abc",
		"b":    true,
		"i":    int64(-2),
		"u":    uint64(1 << 63),
		"f":    1.5,
		"n":    nil,
		"t":    time.Unix(1700000000, 0),
		"uuid": UUID{1, 2, 3},
		"arr":  []any{"x", Dictionary{"k": "v"}, nil},
		"d":    Dictionary{"nested": []byte("hi")},
	}
	m := roundTrip(t, sent)
	if !reflect.DeepEqual(m.Dictionary, sent) {
		t.Fatalf("received %#v, want %#v", m.Dictionary, sent)
	}
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestLoopbackLargeData(t *testing.T) {
	// Data above inlineDataMax travels in a memory entry port.
	m := roundTrip(t, Dictionary{"big": pattern(100_000)})
	if got := m.Data("big"); !bytes.Equal(got, pattern(100_000)) {
		t.Fatalf("big data mismatch: got %d bytes", len(got))
	}
}

func TestLoopbackLargeBody(t *testing.T) {
	// A body larger than maxInlineMessage travels out of line.
	sent := Dictionary{}
	for i := range 10 {
		sent["s"+string(rune('0'+i))] = strings.Repeat(string(rune('a'+i)), 3000)
	}
	m := roundTrip(t, sent)
	if !reflect.DeepEqual(m.Dictionary, sent) {
		t.Fatal("large body mismatch")
	}
}

func TestLoopbackFD(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("hello from go"); err != nil {
		t.Fatal(err)
	}
	checkClose(t, w)
	m := roundTrip(t, Dictionary{"fd": r})
	checkClose(t, r)
	f := m.File("fd")
	if f == nil {
		t.Fatal("no fd in message")
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello from go" {
		t.Fatalf("fd content = %q", b)
	}
}

func TestLoopbackEndpoint(t *testing.T) {
	// The peer connects back to an endpoint in the request, sends updates
	// on it, and then replies, as the API server does for progress.
	c, p := loopback(t)
	updates, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, updates)

	served := make(chan error, 1)
	go func() {
		served <- func() error {
			m, err := p.Receive(5 * time.Second)
			if err != nil {
				return err
			}
			defer checkClose(t, m)
			ep, ok := m.Dictionary["ep"].(Endpoint)
			if !ok {
				return errors.New("no endpoint in request")
			}
			back, err := DialEndpoint(ep)
			if err != nil {
				return err
			}
			defer checkClose(t, back)
			for i := range 3 {
				if err := back.Send(Dictionary{"update": fmt.Sprintf("progress %d", i)}); err != nil {
					return err
				}
			}
			return p.Reply(m, Dictionary{"answer": "42"})
		}()
	}()

	reply, err := c.Request(Dictionary{"ep": updates.Endpoint()}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, reply)
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if got := reply.String("answer"); got != "42" {
		t.Fatalf("answer = %q", got)
	}
	up, err := updates.Accept(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, up)
	for i := range 3 {
		m, err := up.Receive(time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := m.String("update"), fmt.Sprintf("progress %d", i); got != want {
			t.Fatalf("update %d = %q, want %q", i, got, want)
		}
		checkClose(t, m)
	}
}

func TestListenerPeerClose(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, l)
	c, err := DialEndpoint(l.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	p, err := l.Accept(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, p)
	if err := c.Send(Dictionary{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	m, err := p.Receive(time.Second)
	if err != nil || m.String("hello") != "world" {
		t.Fatalf("receive: %v %v", m, err)
	}
	checkClose(t, c)
	if _, err := p.Receive(time.Second); err != ErrClosed {
		t.Fatalf("after close: err = %v, want ErrClosed", err)
	}
}

func TestRequestPeerGone(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, l)
	c, err := DialEndpoint(l.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, c)
	go func() {
		p, err := l.Accept(time.Second)
		if err != nil {
			return
		}
		m, err := p.Receive(time.Second)
		if err != nil {
			return
		}
		checkClose(t, m) // drops the reply right without replying
		checkClose(t, p)
	}()
	if _, err := c.Request(Dictionary{"q": "?"}, 2*time.Second); err != ErrPeerGone {
		t.Fatalf("err = %v, want ErrPeerGone", err)
	}
}

func TestRequestContextCancel(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, l)
	c, err := DialEndpoint(l.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, c)
	got := make(chan *Message, 1)
	go func() {
		p, err := l.Accept(time.Second)
		if err != nil {
			return
		}
		m, err := p.Receive(time.Second)
		if err != nil {
			return
		}
		got <- m // holds the reply right and never replies
	}()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err = c.RequestContext(ctx, Dictionary{"route": "never"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("cancel took %s", d)
	}
	m := <-got
	checkClose(t, m)

	// The connection still works after a cancelled request.
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.RequestContext(ctx, Dictionary{"route": "again"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second request: err = %v, want context.DeadlineExceeded", err)
	}
}

func fillSendQueue(t *testing.T, port mach.Port) int {
	t.Helper()
	body, _, err := encodeMessage(Dictionary{})
	if err != nil {
		t.Fatal(err)
	}
	msg := buildMessage(mach.Header{
		Bits:       mach.MsgBits(mach.MsgTypeCopySend, 0),
		RemotePort: port,
		ID:         msgIDMessage,
	}, nil, body, false)
	for n := range 1024 {
		err := mach.Msg(msg, mach.SendMsg|mach.SendTimeout, uint32(len(msg)), 0, 0)
		if err == mach.Error(mach.SendTimedOut) {
			if err := mach.DestroyUnsent(msg); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("Mach send queue did not fill")
	return 0
}

func TestRequestContextTimeoutWhileSending(t *testing.T) {
	c, p := loopback(t)
	queued := fillSendQueue(t, c.send)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		reply, err := c.RequestContext(ctx, Dictionary{
			"data": pattern(inlineDataMax + 1),
			"text": strings.Repeat("x", maxInlineMessage),
		})
		done <- joinErrors(err, reply.Close())
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("request returned %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request remained blocked on the full queue")
	}
	for range queued {
		m, err := p.Receive(time.Second)
		if err != nil {
			t.Fatal(err)
		}
		checkClose(t, m)
	}
	if m, err := p.Receive(10 * time.Millisecond); !errors.As(err, new(*TimeoutError)) {
		if m != nil {
			checkClose(t, m)
		}
		t.Fatalf("receive after draining the queue returned %v, want timeout", err)
	}
}

func TestRequestContextQueueRetryPreservesMessage(t *testing.T) {
	c, p := loopback(t)
	queued := fillSendQueue(t, c.send)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	data := pattern(inlineDataMax + 1)
	largeText := strings.Repeat("x", maxInlineMessage)
	done := make(chan error, 1)
	go func() {
		reply, err := c.RequestContext(ctx, Dictionary{"data": data, "text": largeText, "fd": os.Stdout})
		if err == nil && !reply.Bool("ok") {
			err = errors.New("missing reply")
		}
		done <- joinErrors(err, reply.Close())
	}()
	// Keep the queue full past the first Mach send timeout.
	time.Sleep(125 * time.Millisecond)
	for range queued {
		m, err := p.Receive(time.Second)
		if err != nil {
			t.Fatal(err)
		}
		checkClose(t, m)
	}
	m, err := p.Receive(time.Second)
	if err != nil {
		select {
		case sendErr := <-done:
			t.Fatalf("receive retry: %v (request returned %v)", err, sendErr)
		default:
			t.Fatalf("receive retry: %v (request still pending)", err)
		}
	}
	defer checkClose(t, m)
	if !bytes.Equal(m.Data("data"), data) || m.String("text") != largeText || m.File("fd") == nil {
		t.Error("retry lost message data or its file descriptor")
	}
	if err := p.Reply(m, Dictionary{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDialContextTimeoutWhileSendingHandshake(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, l) })
	fillSendQueue(t, l.port)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := dialPort(ctx, "endpoint", l.port)
		if c != nil {
			err = joinErrors(err, c.Close())
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("dial returned %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dial remained blocked on the full queue")
	}
}

func TestListenerContext(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, l)

	// Accept gives up when its context ends.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = l.AcceptContext(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcceptContext: err = %v, want context.DeadlineExceeded", err)
	}

	// A stale cancel message does not end a later call.
	c, err := DialEndpoint(l.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, c)
	p, err := l.AcceptContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer checkClose(t, p)

	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = p.ReceiveContext(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReceiveContext: err = %v, want context.DeadlineExceeded", err)
	}
	wake(p.in) // a stale cancel message left in the queue
	if err := c.Send(Dictionary{"n": int64(1)}); err != nil {
		t.Fatal(err)
	}
	m, err := p.ReceiveContext(context.Background())
	if err != nil || m.Int64("n") != 1 {
		t.Fatalf("ReceiveContext after a stale cancel: %v %v", m, err)
	}
	checkClose(t, m)

	checkClose(t, c)
	if _, err := p.ReceiveContext(context.Background()); err != ErrClosed {
		t.Fatalf("after close: err = %v, want ErrClosed", err)
	}
}

// checkClose closes c and reports a failure to t. It is safe to call from
// other goroutines, and it is suitable for defer.
func checkClose(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}
