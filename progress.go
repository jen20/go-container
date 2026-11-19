package container

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jen20/go-container/internal/xpc"
)

// ProgressEventKind is the kind of a progress update.
type ProgressEventKind string

// Progress event kinds. The Set kinds replace a value, and the Add kinds add
// to it.
const (
	ProgressSetDescription    ProgressEventKind = "setDescription"
	ProgressSetSubDescription ProgressEventKind = "setSubDescription"
	ProgressSetItemsName      ProgressEventKind = "setItemsName"
	ProgressAddTasks          ProgressEventKind = "addTasks"
	ProgressSetTasks          ProgressEventKind = "setTasks"
	ProgressAddTotalTasks     ProgressEventKind = "addTotalTasks"
	ProgressSetTotalTasks     ProgressEventKind = "setTotalTasks"
	ProgressAddItems          ProgressEventKind = "addItems"
	ProgressSetItems          ProgressEventKind = "setItems"
	ProgressAddTotalItems     ProgressEventKind = "addTotalItems"
	ProgressSetTotalItems     ProgressEventKind = "setTotalItems"
	ProgressAddSize           ProgressEventKind = "addSize"
	ProgressSetSize           ProgressEventKind = "setSize"
	ProgressAddTotalSize      ProgressEventKind = "addTotalSize"
	ProgressSetTotalSize      ProgressEventKind = "setTotalSize"
)

// ProgressEvent is one progress update. Text is set for the description and
// items name kinds. Value is set for the other kinds. Sizes are in bytes.
type ProgressEvent struct {
	Kind  ProgressEventKind
	Text  string
	Value int64
}

// ProgressFunc receives progress updates. The client calls it from one
// goroutine at a time, in the order that the server sent the updates.
type ProgressFunc func(events []ProgressEvent)

// progressKeys lists the message keys in the order that they are read. The
// key is "progressUpdate" followed by the kind, with the first letter in
// upper case.
var progressKeys = []struct {
	key  string
	kind ProgressEventKind
	text bool
}{
	{"progressUpdateSetDescription", ProgressSetDescription, true},
	{"progressUpdateSetSubDescription", ProgressSetSubDescription, true},
	{"progressUpdateSetItemsName", ProgressSetItemsName, true},
	{"progressUpdateAddTasks", ProgressAddTasks, false},
	{"progressUpdateSetTasks", ProgressSetTasks, false},
	{"progressUpdateAddTotalTasks", ProgressAddTotalTasks, false},
	{"progressUpdateSetTotalTasks", ProgressSetTotalTasks, false},
	{"progressUpdateAddItems", ProgressAddItems, false},
	{"progressUpdateSetItems", ProgressSetItems, false},
	{"progressUpdateAddTotalItems", ProgressAddTotalItems, false},
	{"progressUpdateSetTotalItems", ProgressSetTotalItems, false},
	{"progressUpdateAddSize", ProgressAddSize, false},
	{"progressUpdateSetSize", ProgressSetSize, false},
	{"progressUpdateAddTotalSize", ProgressAddTotalSize, false},
	{"progressUpdateSetTotalSize", ProgressSetTotalSize, false},
}

// parseProgress converts one update message into events. It skips numbers
// that are zero.
func parseProgress(d xpc.Dictionary) []ProgressEvent {
	var events []ProgressEvent
	for _, k := range progressKeys {
		if k.text {
			if s, ok := d[k.key].(string); ok {
				events = append(events, ProgressEvent{Kind: k.kind, Text: s})
			}
			continue
		}
		if v := d.Int64(k.key); v != 0 {
			events = append(events, ProgressEvent{Kind: k.kind, Value: v})
		}
	}
	return events
}

// Progress is the state that a sequence of progress events describes. Use
// it in a ProgressFunc to keep track of an operation.
type Progress struct {
	Description    string
	SubDescription string
	ItemsName      string
	Tasks          int64
	TotalTasks     int64
	Items          int64
	TotalItems     int64
	Size           int64
	TotalSize      int64
}

// Apply updates the state with events.
func (p *Progress) Apply(events []ProgressEvent) {
	for _, e := range events {
		switch e.Kind {
		case ProgressSetDescription:
			p.Description = e.Text
		case ProgressSetSubDescription:
			p.SubDescription = e.Text
		case ProgressSetItemsName:
			p.ItemsName = e.Text
		case ProgressAddTasks:
			p.Tasks += e.Value
		case ProgressSetTasks:
			p.Tasks = e.Value
		case ProgressAddTotalTasks:
			p.TotalTasks += e.Value
		case ProgressSetTotalTasks:
			p.TotalTasks = e.Value
		case ProgressAddItems:
			p.Items += e.Value
		case ProgressSetItems:
			p.Items = e.Value
		case ProgressAddTotalItems:
			p.TotalItems += e.Value
		case ProgressSetTotalItems:
			p.TotalItems = e.Value
		case ProgressAddSize:
			p.Size += e.Value
		case ProgressSetSize:
			p.Size = e.Value
		case ProgressAddTotalSize:
			p.TotalSize += e.Value
		case ProgressSetTotalSize:
			p.TotalSize = e.Value
		}
	}
}

// progressReceiver receives the progress updates of one request. It listens
// on an anonymous endpoint. The server connects to the endpoint and sends
// one-way update messages while it handles the request.
type progressReceiver struct {
	l      *xpc.Listener
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	errs []error // failures of the receive goroutines, reported by stop
}

// fail records an error for stop to report. A nil err is ignored.
func (r *progressReceiver) fail(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

// startProgress starts a receiver for fn and adds its endpoint to values. If
// fn is nil, it does nothing and returns a receiver whose stop does nothing.
func startProgress(fn ProgressFunc, values xpc.Dictionary) (*progressReceiver, error) {
	if fn == nil {
		return nil, nil //nolint:nilnil // a nil receiver is valid, and its stop does nothing
	}
	l, err := xpc.Listen()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &progressReceiver{l: l, cancel: cancel, done: make(chan struct{})}
	values["progressUpdateEndpoint"] = l.Endpoint()

	var mu sync.Mutex // calls fn one at a time
	deliver := func(m *xpc.Message) {
		events := parseProgress(m.Dictionary)
		r.fail(m.Close())
		if len(events) > 0 {
			mu.Lock()
			fn(events)
			mu.Unlock()
		}
	}
	go func() {
		defer close(r.done)
		var peers sync.WaitGroup
		defer peers.Wait()
		for {
			p, err := l.AcceptContext(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r.fail(err)
				}
				return
			}
			peers.Go(func() {
				defer func() { r.fail(p.Close()) }()
				for {
					m, err := p.ReceiveContext(ctx)
					if err != nil {
						if ctx.Err() != nil {
							// Deliver updates that were sent before the
							// reply arrived. The queue is empty when the
							// receive times out.
							for {
								m, err := p.Receive(time.Millisecond)
								if err != nil {
									if !isTimeout(err) && !errors.Is(err, xpc.ErrClosed) {
										r.fail(err)
									}
									return
								}
								deliver(m)
							}
						}
						if !errors.Is(err, xpc.ErrClosed) {
							r.fail(err)
						}
						return
					}
					deliver(m)
				}
			})
		}
	}()
	return r, nil
}

// stop ends the receiver after the request is done. It delivers the updates
// that are already queued, then closes the endpoint. It returns the errors
// of the receiver, which are failures to receive updates or to release
// their resources.
func (r *progressReceiver) stop() error {
	if r == nil {
		return nil
	}
	r.cancel()
	<-r.done
	r.fail(r.l.Close())
	r.mu.Lock()
	defer r.mu.Unlock()
	return joinErrors(r.errs...)
}

func isTimeout(err error) bool {
	var te *xpc.TimeoutError
	return errors.As(err, &te)
}
