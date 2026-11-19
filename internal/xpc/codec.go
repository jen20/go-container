package xpc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/jen20/go-container/internal/mach"
)

var le = binary.LittleEndian

// portDesc is a port that travels in a Mach port descriptor.
type portDesc struct {
	name        mach.Port
	disposition uint8
}

type encoder struct {
	buf   []byte
	ports []portDesc
}

func pad4(n int) int { return (n + 3) &^ 3 }

func (e *encoder) u32(v uint32) { e.buf = le.AppendUint32(e.buf, v) }
func (e *encoder) u64(v uint64) { e.buf = le.AppendUint64(e.buf, v) }

func (e *encoder) bytesPadded(b []byte) {
	e.buf = append(e.buf, b...)
	for range pad4(len(b)) - len(b) {
		e.buf = append(e.buf, 0)
	}
}

func (e *encoder) cstring(s string) {
	b := make([]byte, len(s)+1)
	copy(b, s)
	e.bytesPadded(b)
}

// encodeMessage serializes a root dictionary. The caller owns the returned
// ports and must send or release them.
func encodeMessage(d Dictionary) ([]byte, []portDesc, error) {
	e := &encoder{buf: make([]byte, 0, 256)}
	e.u32(wireMagic)
	e.u32(wireVersion)
	if err := e.value(d); err != nil {
		return nil, nil, joinErrors(err, releasePorts(e.ports))
	}
	return e.buf, e.ports, nil
}

func releasePorts(ports []portDesc) error {
	var errs []error
	for _, p := range ports {
		if p.disposition == mach.MsgTypeMoveSend {
			errs = append(errs, mach.Deallocate(p.name))
		}
	}
	return joinErrors(errs...)
}

// container writes a type tag and a length placeholder, runs body, and then
// fills in the length of everything after the length field.
func (e *encoder) container(typ uint32, body func() error) error {
	e.u32(typ)
	at := len(e.buf)
	e.u32(0)
	if err := body(); err != nil {
		return err
	}
	le.PutUint32(e.buf[at:], uint32(len(e.buf)-at-4))
	return nil
}

func (e *encoder) value(v any) error {
	switch v := v.(type) {
	case nil:
		e.u32(typeNull)
	case bool:
		e.u32(typeBool)
		if v {
			e.u32(1)
		} else {
			e.u32(0)
		}
	case int64:
		e.u32(typeInt64)
		e.u64(uint64(v))
	case int:
		e.u32(typeInt64)
		e.u64(uint64(v))
	case uint64:
		e.u32(typeUint64)
		e.u64(v)
	case float64:
		e.u32(typeDouble)
		e.u64(math.Float64bits(v))
	case time.Time:
		e.u32(typeDate)
		e.u64(uint64(v.UnixNano()))
	case string:
		e.u32(typeString)
		e.u32(uint32(len(v) + 1))
		e.cstring(v)
	case []byte:
		e.u32(typeData)
		e.u32(uint32(len(v)))
		if len(v) > inlineDataMax {
			p, err := mach.NewMemoryEntry(v)
			if err != nil {
				return err
			}
			e.ports = append(e.ports, portDesc{p, mach.MsgTypeMoveSend})
		} else {
			e.bytesPadded(v)
		}
	case UUID:
		e.u32(typeUUID)
		e.buf = append(e.buf, v[:]...)
	case *os.File:
		p, err := mach.FileportMake(int(v.Fd()))
		if err != nil {
			return err
		}
		e.u32(typeFD)
		e.ports = append(e.ports, portDesc{p, mach.MsgTypeMoveSend})
	case Endpoint:
		e.u32(typeEndpoint)
		e.ports = append(e.ports, portDesc{v.Port, mach.MsgTypeCopySend})
	case []any:
		return e.container(typeArray, func() error {
			e.u32(uint32(len(v)))
			for _, item := range v {
				if err := e.value(item); err != nil {
					return err
				}
			}
			return nil
		})
	case Dictionary:
		return e.container(typeDictionary, func() error {
			e.u32(uint32(len(v)))
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				e.cstring(k)
				if err := e.value(v[k]); err != nil {
					return err
				}
			}
			return nil
		})
	case map[string]any:
		return e.value(Dictionary(v))
	default:
		return &UnsupportedTypeError{Type: fmt.Sprintf("%T", v)}
	}
	return nil
}

var errShort = errors.New("xpc: message truncated")

type decoder struct {
	buf   []byte
	off   int
	ports []mach.Port
}

func (d *decoder) u32() (uint32, error) {
	if d.off+4 > len(d.buf) {
		return 0, errShort
	}
	v := le.Uint32(d.buf[d.off:])
	d.off += 4
	return v, nil
}

func (d *decoder) u64() (uint64, error) {
	if d.off+8 > len(d.buf) {
		return 0, errShort
	}
	v := le.Uint64(d.buf[d.off:])
	d.off += 8
	return v, nil
}

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || d.off+n > len(d.buf) {
		return nil, errShort
	}
	b := d.buf[d.off : d.off+n]
	d.off += pad4(n)
	if d.off > len(d.buf) {
		d.off = len(d.buf)
	}
	return b, nil
}

func (d *decoder) port() (mach.Port, error) {
	if len(d.ports) == 0 {
		return 0, errors.New("xpc: message has fewer ports than values")
	}
	p := d.ports[0]
	d.ports = d.ports[1:]
	return p, nil
}

func (d *decoder) cstring() (string, error) {
	start := d.off
	for i := d.off; i < len(d.buf); i++ {
		if d.buf[i] == 0 {
			s := string(d.buf[start:i])
			d.off = start + pad4(i-start+1)
			return s, nil
		}
	}
	return "", errShort
}

// decodeMessage parses a serialized message. It takes ownership of ports.
func decodeMessage(body []byte, ports []mach.Port) (dict Dictionary, err error) {
	d := &decoder{buf: body, ports: ports}
	defer func() {
		var errs []error
		for _, p := range d.ports {
			errs = append(errs, mach.Deallocate(p))
		}
		if cleanupErr := joinErrors(errs...); cleanupErr != nil {
			err = joinErrors(err, cleanupErr, dict.Close())
			dict = nil
		}
	}()
	magic, err := d.u32()
	if err != nil {
		return nil, err
	}
	if magic != wireMagic {
		return nil, fmt.Errorf("xpc: bad magic 0x%08x", magic)
	}
	if version, err := d.u32(); err != nil {
		return nil, err
	} else if version != wireVersion {
		return nil, fmt.Errorf("xpc: unsupported wire version %d", version)
	}
	v, err := d.value()
	if err != nil {
		return nil, err
	}
	dict, ok := v.(Dictionary)
	if !ok {
		return nil, joinErrors(fmt.Errorf("xpc: root object is %T, not a dictionary", v), closeValue(v))
	}
	return dict, nil
}

func (d *decoder) value() (any, error) {
	typ, err := d.u32()
	if err != nil {
		return nil, err
	}
	switch typ {
	case typeNull:
		return nil, nil //nolint:nilnil // a nil value is the decoded XPC null
	case typeBool:
		v, err := d.u32()
		return v != 0, err
	case typeInt64:
		v, err := d.u64()
		return int64(v), err
	case typeUint64:
		return d.u64()
	case typeDouble:
		v, err := d.u64()
		return math.Float64frombits(v), err
	case typeDate:
		v, err := d.u64()
		return time.Unix(0, int64(v)), err
	case typeString:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		b, err := d.take(int(n))
		if err != nil {
			return nil, err
		}
		if n > 0 && b[n-1] == 0 {
			b = b[:n-1]
		}
		return string(b), nil
	case typeData:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		if n > inlineDataMax {
			p, err := d.port()
			if err != nil {
				return nil, err
			}
			b, err := mach.ReadMemoryEntry(p, uint64(n))
			if err := joinErrors(err, mach.Deallocate(p)); err != nil {
				return nil, err
			}
			return b, nil
		}
		b, err := d.take(int(n))
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case typeUUID:
		b, err := d.take(16)
		if err != nil {
			return nil, err
		}
		var u UUID
		copy(u[:], b)
		return u, nil
	case typeFD:
		p, err := d.port()
		if err != nil {
			return nil, err
		}
		fd, err := mach.FileportFD(p)
		if err != nil {
			return nil, joinErrors(err, mach.Deallocate(p))
		}
		f := os.NewFile(uintptr(fd), "xpc-fd")
		if err := mach.Deallocate(p); err != nil {
			return nil, joinErrors(err, f.Close())
		}
		return f, nil
	case typeEndpoint:
		p, err := d.port()
		if err != nil {
			return nil, err
		}
		return Endpoint{Port: p}, nil
	case typeArray:
		if _, err := d.u32(); err != nil { // byte length
			return nil, err
		}
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		arr := make([]any, 0, min(n, 1024))
		for range n {
			v, err := d.value()
			if err != nil {
				errs := []error{err}
				for _, item := range arr {
					errs = append(errs, closeValue(item))
				}
				return nil, joinErrors(errs...)
			}
			arr = append(arr, v)
		}
		return arr, nil
	case typeDictionary:
		if _, err := d.u32(); err != nil { // byte length
			return nil, err
		}
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		dict := make(Dictionary, min(n, 1024))
		for range n {
			k, err := d.cstring()
			if err != nil {
				return nil, joinErrors(err, dict.Close())
			}
			v, err := d.value()
			if err != nil {
				return nil, joinErrors(err, dict.Close())
			}
			previous := dict[k]
			dict[k] = v
			if err := closeValue(previous); err != nil {
				return nil, joinErrors(err, dict.Close())
			}
		}
		return dict, nil
	default:
		return nil, &UnsupportedTypeError{Type: fmt.Sprintf("0x%x", typ)}
	}
}
