package xpc

import (
	"errors"
	"runtime"
	"unsafe"
)

// msgIDReply is the message ID of a reply.
const msgIDReply = 0x20000000 // the ID that libxpc puts on replies

func bodyAddr(b []byte) uintptr { return uintptr(unsafe.Pointer(&b[0])) }

func keepAlive(b []byte) { runtime.KeepAlive(b) }

// joinErrors is errors.Join, but it returns a single non-nil error
// unchanged, so that callers can still compare it or check its type. Use it
// to add the errors of cleanup steps to the error that caused them.
func joinErrors(errs ...error) error {
	var nonNil []error
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	}
	return errors.Join(nonNil...)
}
