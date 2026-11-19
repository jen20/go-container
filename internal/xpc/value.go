// Package xpc is a pure Go implementation of the macOS XPC wire protocol over
// Mach messages.
package xpc

// The package calls libSystem only for Mach traps, through the mach package.

import (
	"fmt"
	"os"
	"time"

	"github.com/jen20/go-container/internal/mach"
)

// Dictionary is an XPC dictionary. Values are one of: nil (XPC null), bool,
// int64, uint64, float64, string, []byte, time.Time, UUID, *os.File, Endpoint,
// []any, or Dictionary.
type Dictionary map[string]any

// UUID is an XPC UUID.
type UUID [16]byte

// Endpoint is an XPC endpoint. It holds a send right to a peer listener.
type Endpoint struct {
	Port mach.Port
}

// Wire type tags.
const (
	typeNull       = 0x1000
	typeBool       = 0x2000
	typeInt64      = 0x3000
	typeUint64     = 0x4000
	typeDouble     = 0x5000
	typePointer    = 0x6000
	typeDate       = 0x7000
	typeData       = 0x8000
	typeString     = 0x9000
	typeUUID       = 0xa000
	typeFD         = 0xb000
	typeShmem      = 0xc000
	typeMachSend   = 0xd000
	typeArray      = 0xe000
	typeDictionary = 0xf000
	typeError      = 0x10000
	typeConnection = 0x11000
	typeEndpoint   = 0x12000
)

const (
	wireMagic   = 0x40585043 // "CPX@"
	wireVersion = 5

	// inlineDataMax is the largest data value that is sent inline. A larger
	// value travels as a memory entry port.
	inlineDataMax = 0x4000
)

// String returns a string value, or "" if the key is missing or not a string.
func (d Dictionary) String(key string) string {
	s, _ := d[key].(string)
	return s
}

// Data returns a data value, or nil.
func (d Dictionary) Data(key string) []byte {
	b, _ := d[key].([]byte)
	return b
}

// Bool returns a bool value, or false.
func (d Dictionary) Bool(key string) bool {
	b, _ := d[key].(bool)
	return b
}

// Int64 returns an int64 value, or 0.
func (d Dictionary) Int64(key string) int64 {
	i, _ := d[key].(int64)
	return i
}

// Uint64 returns a uint64 value, or 0.
func (d Dictionary) Uint64(key string) uint64 {
	u, _ := d[key].(uint64)
	return u
}

// Date returns a date value, or the zero time.
func (d Dictionary) Date(key string) time.Time {
	t, _ := d[key].(time.Time)
	return t
}

// File returns a file descriptor value, or nil.
func (d Dictionary) File(key string) *os.File {
	f, _ := d[key].(*os.File)
	return f
}

// Close releases every file descriptor and port that the dictionary holds.
// It releases all of them even if some fail, and returns the errors joined.
func (d Dictionary) Close() error {
	var errs []error
	for _, v := range d {
		errs = append(errs, closeValue(v))
	}
	return joinErrors(errs...)
}

func closeValue(v any) error {
	switch v := v.(type) {
	case *os.File:
		return v.Close()
	case Endpoint:
		return mach.Deallocate(v.Port)
	case Dictionary:
		return v.Close()
	case []any:
		var errs []error
		for _, e := range v {
			errs = append(errs, closeValue(e))
		}
		return joinErrors(errs...)
	}
	return nil
}

// UnsupportedTypeError reports a value that this package cannot encode or decode.
type UnsupportedTypeError struct {
	Type any
}

func (e *UnsupportedTypeError) Error() string {
	return fmt.Sprintf("xpc: unsupported type %v", e.Type)
}
