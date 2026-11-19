// Package container is a Go client for the Apple container API server
// (com.apple.container.apiserver).
package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/jen20/go-container/internal/mach"
	"github.com/jen20/go-container/internal/xpc"
)

// ServiceName is the Mach service name of the container API server.
const ServiceName = "com.apple.container.apiserver"

// ImagesServiceName is the Mach service name of the image service plugin.
const ImagesServiceName = "com.apple.container.core.container-core-images"

// DefaultTimeout is the timeout for most requests when the context has
// no earlier deadline. Some requests, such as waiting for a process, have no
// timeout and use only the context.
const DefaultTimeout = 60 * time.Second

// Message keys of the API server protocol.
const (
	routeKey = "com.apple.container.xpc.route"
	errorKey = "com.apple.container.xpc.error"
)

// Client is a connection to the container API server. It is safe for
// concurrent use. The client connects on the first request. If the server
// restarts, the request that finds the old connection gone fails, and the
// next request connects again.
//
// The client also has a system configuration, which sets defaults such as
// the registry of short image names. See the Option functions.
type Client struct {
	api    service // connection to the API server
	images service // connection to the image service

	configMu  sync.Mutex
	loader    ConfigLoader
	sysConfig *SystemConfig // nil until loaded, or with no loader
}

// NewClient returns a client for the container API server. It does not
// connect until the first request. With no options, the client uses
// DefaultSystemConfig and reads no configuration files.
func NewClient(opts ...Option) *Client {
	return NewServiceClient(ServiceName, opts...)
}

// NewServiceClient returns a client for an API server that is registered
// under a different Mach service name.
func NewServiceClient(name string, opts ...Option) *Client {
	c := &Client{api: service{name: name}, images: service{name: ImagesServiceName}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Close closes the connections. Requests after Close return xpc.ErrClosed.
func (c *Client) Close() error {
	return joinErrors(c.images.Close(), c.api.Close())
}

// service is a connection to one Mach service. It connects on the first
// request. If the server end goes away, the next request connects again.
type service struct {
	name string

	mu      sync.Mutex
	conn    *xpc.Conn
	dialing chan struct{}
	closed  bool
}

// Close closes the connection. Requests after Close return xpc.ErrClosed.
func (s *service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

func (s *service) connection(ctx context.Context) (*xpc.Conn, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, xpc.ErrClosed
		}
		if s.conn != nil {
			conn := s.conn
			s.mu.Unlock()
			return conn, nil
		}
		dialing := s.dialing
		if dialing == nil {
			s.dialing = make(chan struct{})
			s.mu.Unlock()
			return s.dial(ctx)
		}
		s.mu.Unlock()
		select {
		case <-dialing:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *service) dial(ctx context.Context) (*xpc.Conn, error) {
	conn, err := xpc.DialContext(ctx, s.name)
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.dialing)
	s.dialing = nil
	if err != nil {
		if errors.Is(err, xpc.ErrUnknownService) {
			return nil, &Error{
				Code:    CodeInterrupted,
				Message: fmt.Sprintf("%s is not running; start it with `container system start`", s.name),
				cause:   err,
			}
		}
		return nil, err
	}
	if s.closed {
		return nil, joinErrors(xpc.ErrClosed, conn.Close())
	}
	s.conn = conn
	return conn, nil
}

// dropConnection forgets conn if it is still the current connection, so that
// the next request dials again. It returns the error from closing conn.
func (s *service) dropConnection(conn *xpc.Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != conn {
		return nil
	}
	s.conn = nil
	return conn.Close()
}

// connectionLost reports whether err means that the server end of the
// connection is gone.
func connectionLost(err error) bool {
	return errors.Is(err, xpc.ErrPeerGone) || errors.Is(err, mach.Error(mach.SendInvalidDest))
}

// request sends a message for route with DefaultTimeout. See requestTimeout.
func (s *service) request(ctx context.Context, route string, values xpc.Dictionary) (xpc.Dictionary, error) {
	return s.requestTimeout(ctx, route, values, DefaultTimeout)
}

// requestTimeout sends a message for route with the given values and waits
// for the reply until ctx is done or timeout passes. A zero timeout waits
// until ctx is done. The caller must close the reply. A server error becomes
// an *Error.
func (s *service) requestTimeout(ctx context.Context, route string, values xpc.Dictionary, timeout time.Duration) (xpc.Dictionary, error) {
	p, err := s.start(ctx, route, values, timeout)
	if err != nil {
		return nil, err
	}
	return p.wait()
}

// pending is a request that was sent and whose reply has not been received.
type pending struct {
	s      *service
	ctx    context.Context
	conn   *xpc.Conn
	call   *xpc.Call
	route  string
	cancel context.CancelFunc
}

// start sends a request and returns without waiting for the reply. When it
// returns, the message is queued at the server ahead of any later message.
// The caller must call wait.
func (s *service) start(ctx context.Context, route string, values xpc.Dictionary, timeout time.Duration) (*pending, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	msg := xpc.Dictionary{routeKey: route}
	maps.Copy(msg, values)
	p := &pending{s: s, ctx: ctx, route: route}
	// Each request has its own context, so that it can be cancelled alone.
	var reqCtx context.Context
	if timeout > 0 {
		reqCtx, p.cancel = context.WithTimeout(ctx, timeout)
	} else {
		reqCtx, p.cancel = context.WithCancel(ctx)
	}
	var err error
	p.conn, err = s.connection(reqCtx)
	if err == nil {
		p.call, err = p.conn.Go(reqCtx, msg)
	}
	if err != nil {
		p.cancel()
		return nil, p.mapError(err)
	}
	return p, nil
}

// wait waits for the reply of a started request.
func (p *pending) wait() (xpc.Dictionary, error) {
	defer p.cancel()
	reply, err := p.call.Wait()
	if err != nil {
		return nil, p.mapError(err)
	}
	if data := reply.Data(errorKey); data != nil {
		return nil, joinErrors(decodeServerError(data), reply.Close())
	}
	return reply, nil
}

func (p *pending) mapError(err error) error {
	if p.ctx.Err() != nil {
		return p.ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Message: fmt.Sprintf("XPC timeout for request to %s/%s", p.s.name, p.route), cause: err}
	}
	if connectionLost(err) {
		// Drop the connection so that the next request dials again. This
		// is how an XPC connection behaves after an interruption.
		cause := err
		if p.conn != nil {
			cause = joinErrors(err, p.s.dropConnection(p.conn))
		}
		return &Error{Code: CodeInterrupted, Message: "connection to " + p.s.name + " was interrupted", cause: cause}
	}
	return err
}

// requestJSON sends a request and decodes the JSON data under replyKey into
// out. If the reply has no value under replyKey, it returns false.
func (s *service) requestJSON(ctx context.Context, route string, values xpc.Dictionary, replyKey string, out any) (bool, error) {
	return s.requestJSONTimeout(ctx, route, values, replyKey, out, DefaultTimeout)
}

// requestJSONTimeout is requestJSON with a timeout. Zero means no timeout.
func (s *service) requestJSONTimeout(ctx context.Context, route string, values xpc.Dictionary, replyKey string, out any, timeout time.Duration) (_ bool, err error) {
	reply, err := s.requestTimeout(ctx, route, values, timeout)
	if err != nil {
		return false, err
	}
	defer closeJoin(&err, reply)
	data := reply.Data(replyKey)
	if data == nil {
		return false, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false, fmt.Errorf("client: decode %s reply: %w", route, err)
	}
	return true, nil
}

// decodeReply decodes the JSON data under key in a reply to route. If the
// reply has no value under key, it returns an error with CodeEmpty.
func decodeReply(reply xpc.Dictionary, route, key string, out any) error {
	data := reply.Data(key)
	if data == nil {
		return &Error{Code: CodeEmpty, Message: key + " not received"}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("client: decode %s reply: %w", route, err)
	}
	return nil
}

// encodeJSON encodes v for a request value.
func encodeJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("client: encode request: %w", err)
	}
	return b, nil
}
