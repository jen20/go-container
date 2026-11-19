//go:build arm64

// Package mach provides minimal Mach IPC bindings for darwin/arm64, the only
// platform that Apple container runs on.
package mach

// The bindings call libSystem through Go's own dynamic imports. See
// libc_darwin_arm64.go.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// Port is a Mach port name in the current task.
type Port uint32

// PortNull is MACH_PORT_NULL, the name of no port.
const PortNull Port = 0

// Message type dispositions.
const (
	MsgTypeMoveReceive  = 16
	MsgTypeMoveSend     = 17
	MsgTypeMoveSendOnce = 18
	MsgTypeCopySend     = 19
	MsgTypeMakeSend     = 20
	MsgTypeMakeSendOnce = 21
)

// Port rights.
const (
	PortRightSend    = 0
	PortRightReceive = 1
)

// mach_msg options.
const (
	SendMsg       = 0x1
	RcvMsg        = 0x2
	RcvLarge      = 0x4
	SendTimeout   = 0x10
	SendInterrupt = 0x40
	RcvTimeout    = 0x100
)

// Header bits.
const (
	MsgBitsComplex = 0x80000000
)

// Return codes.
const (
	KernSuccess      = 0
	RcvTooLarge      = 0x10004004
	RcvTimedOut      = 0x10004003
	SendInvalidDest  = 0x10000003
	SendTimedOut     = 0x10000004
	SendInterrupted  = 0x10000007
	MsgErrorMask     = 0x00003e00
	RcvPortDied      = 0x10004009
	BootstrapUnknown = 1102
)

// Descriptor types.
const (
	DescPort        = 0
	DescOOL         = 1
	DescOOLPorts    = 2
	DescOOLVolatile = 3
	DescGuardedPort = 4
)

// HeaderSize is sizeof(mach_msg_header_t).
const HeaderSize = 24

// RcvTrailerAudit is the mach_msg option MACH_RCV_TRAILER_AUDIT, which asks
// for an audit trailer on a received message.
const RcvTrailerAudit = 3 << 24

var (
	once sync.Once

	taskSelf      Port
	bootstrapPort Port
)

// VM constants.
const (
	vmFlagsAnywhere = 1
	vmProtRead      = 1
	vmProtWrite     = 2
	vmInheritNone   = 2
	mapMemVMCopy    = 0x200000
)

// PageSize is the VM page size on Apple silicon.
const PageSize = 16384

func load() error {
	once.Do(func() {
		taskSelf = Port(loadTaskSelf())
		bootstrapPort = Port(loadBootstrapPort())
	})
	return nil
}

// Error is a kern_return_t or mach_msg_return_t.
type Error int32

func (e Error) Error() string { return fmt.Sprintf("mach error 0x%x", uint32(e)) }

func kr(r int32) error {
	if r == KernSuccess {
		return nil
	}
	return Error(r)
}

// TaskSelf returns the task port of the current process.
func TaskSelf() Port { _ = load(); return taskSelf }

// BootstrapPort returns the bootstrap port of the current process.
func BootstrapPort() Port { _ = load(); return bootstrapPort }

// AllocateReceive allocates a new port with a receive right.
func AllocateReceive() (Port, error) {
	if err := load(); err != nil {
		return 0, err
	}
	var p Port
	return p, kr(portAllocate(taskSelf, PortRightReceive, &p))
}

// InsertSendRight adds a send right to a port whose receive right this task
// holds.
func InsertSendRight(p Port) error {
	return kr(portInsertRight(taskSelf, p, p, MsgTypeMakeSend))
}

// Deallocate releases a send or send-once right.
func Deallocate(p Port) error { return kr(portDeallocate(taskSelf, p)) }

// DestroyReceive drops a receive right.
func DestroyReceive(p Port) error { return kr(portModRefs(taskSelf, p, PortRightReceive, -1)) }

// LookUp asks launchd for a send right to a named service.
func LookUp(name string) (Port, error) {
	if err := load(); err != nil {
		return 0, err
	}
	var p Port
	return p, kr(bootstrapLookUp(bootstrapPort, name, &p))
}

// CheckIn claims the receive right of a launchd-managed service.
func CheckIn(name string) (Port, error) {
	if err := load(); err != nil {
		return 0, err
	}
	var p Port
	return p, kr(bootstrapCheckIn(bootstrapPort, name, &p))
}

// Notification message IDs.
const (
	NotifyNoSenders = 0x46
	NotifySendOnce  = 0x47
	NotifyDeadName  = 0x48
)

// RequestNoSenders asks the kernel to send a no-senders notification to the
// port itself when its last send right goes away.
func RequestNoSenders(p Port) error {
	var prev Port
	if err := kr(requestNotify(taskSelf, p, NotifyNoSenders, 0, p, MsgTypeMakeSendOnce, &prev)); err != nil {
		return err
	}
	if prev != 0 {
		return Deallocate(prev)
	}
	return nil
}

// VMDeallocate frees out-of-line memory received in a message.
func VMDeallocate(addr, size uintptr) error { return kr(vmDeallocate(taskSelf, addr, size)) }

// FileportMake wraps a file descriptor in a Mach port.
func FileportMake(fd int) (Port, error) {
	if err := load(); err != nil {
		return 0, err
	}
	var p Port
	if r := fileportMakeport(int32(fd), &p); r != 0 {
		return 0, fmt.Errorf("fileport_makeport: %d", r)
	}
	return p, nil
}

// FileportFD returns a new file descriptor for a fileport.
func FileportFD(p Port) (int, error) {
	fd := fileportMakefd(p)
	if fd < 0 {
		return -1, fmt.Errorf("fileport_makefd failed")
	}
	return int(fd), nil
}

// ReadMemoryEntry maps a memory entry port read-only, copies size bytes out of
// it, and unmaps it. It does not release the port.
func ReadMemoryEntry(p Port, size uint64) ([]byte, error) {
	if err := load(); err != nil {
		return nil, err
	}
	var addr uint64
	mapSize := (size + PageSize - 1) &^ (PageSize - 1)
	if err := kr(machVMMap(taskSelf, &addr, mapSize, 0, vmFlagsAnywhere, p, 0, 0, vmProtRead, vmProtRead, vmInheritNone)); err != nil {
		return nil, fmt.Errorf("mach_vm_map: %w", err)
	}
	out := make([]byte, size)
	copy(out, kernelBytes(uintptr(addr), size))
	if err := VMDeallocate(uintptr(addr), uintptr(mapSize)); err != nil {
		return nil, fmt.Errorf("vm_deallocate: %w", err)
	}
	return out, nil
}

// NewMemoryEntry copies data into fresh VM pages and returns a memory entry
// port (a send right) that covers it.
func NewMemoryEntry(data []byte) (Port, error) {
	if err := load(); err != nil {
		return 0, err
	}
	size := (uint64(len(data)) + PageSize - 1) &^ (PageSize - 1)
	var addr uintptr
	if err := kr(vmAllocate(taskSelf, &addr, uintptr(size), vmFlagsAnywhere)); err != nil {
		return 0, fmt.Errorf("vm_allocate: %w", err)
	}
	copy(kernelBytes(addr, size), data)
	var p Port
	entrySize := size
	// The memory entry holds a copy of the pages, so they are freed in all
	// cases.
	makeErr := kr(makeMemoryEntry64(taskSelf, &entrySize, uint64(addr), vmProtRead|mapMemVMCopy, &p, 0))
	freeErr := VMDeallocate(addr, uintptr(size))
	switch {
	case makeErr != nil && freeErr != nil:
		return 0, errors.Join(fmt.Errorf("mach_make_memory_entry_64: %w", makeErr), fmt.Errorf("vm_deallocate: %w", freeErr))
	case makeErr != nil:
		return 0, fmt.Errorf("mach_make_memory_entry_64: %w", makeErr)
	case freeErr != nil:
		freeErr = fmt.Errorf("vm_deallocate: %w", freeErr)
		if err := Deallocate(p); err != nil {
			return 0, errors.Join(freeErr, err)
		}
		return 0, freeErr
	}
	return p, nil
}

// TakeOOL copies an out-of-line region that the kernel mapped into this
// task, and deallocates the region. The caller owns the returned slice. If
// the deallocation fails, TakeOOL still returns the data, with the error.
func TakeOOL(addr uintptr, size int) ([]byte, error) {
	if addr == 0 || size == 0 {
		return nil, nil
	}
	out := make([]byte, size)
	copy(out, kernelBytes(addr, size))
	return out, VMDeallocate(addr, uintptr(size))
}

// kernelBytes returns the size bytes at addr, which is memory that the
// kernel mapped into this task, for example with mach_vm_map or as an
// out-of-line message region. Do not use it for an address of Go memory.
func kernelBytes[N int | uint64](addr uintptr, size N) []byte {
	// The go vet check unsafeptr forbids a uintptr to unsafe.Pointer
	// conversion, because the garbage collector can move or free the Go
	// object that a uintptr once pointed to. Kernel mappings are outside
	// the Go heap, so the collector never moves or frees them, and the
	// address stays valid until vm_deallocate. The conversion is safe.
	// Reading the bits of addr as a pointer, instead of converting the
	// uintptr, keeps go vet quiet.
	return unsafe.Slice(*(**byte)(unsafe.Pointer(&addr)), size)
}

// Msg calls mach_msg on buf, which holds the message to send, the buffer
// for the received message, or both.
func Msg(buf []byte, option int32, sendSize uint32, rcvName Port, timeout uint32) error {
	if err := load(); err != nil {
		return err
	}
	// buf must stay pinned for the call. It does, because the call takes
	// it as a pointer argument.
	return kr(machMsg(unsafe.Pointer(&buf[0]), option, sendSize, uint32(len(buf)), rcvName, timeout, 0))
}

// DestroyUnsent releases the rights and memory that mach_msg copied back
// into buf after a send timeout or interruption. mach_msg_destroy handles
// the remote right and descriptors, and the local field holds a right in a
// returned send.
func DestroyUnsent(buf []byte) error {
	h := ParseHeader(buf)
	machMsgDestroy(unsafe.Pointer(&buf[0]))
	switch (h.Bits >> 8) & 0x1f {
	case MsgTypeMoveSend, MsgTypeMoveSendOnce:
		if h.LocalPort != PortNull && h.LocalPort != ^Port(0) {
			return Deallocate(h.LocalPort)
		}
	}
	return nil
}

// Header is mach_msg_header_t.
type Header struct {
	Bits       uint32
	Size       uint32
	RemotePort Port
	LocalPort  Port
	Voucher    Port
	ID         int32
}

// Put writes h to the first HeaderSize bytes of b.
func (h Header) Put(b []byte) {
	le := binary.LittleEndian
	le.PutUint32(b[0:], h.Bits)
	le.PutUint32(b[4:], h.Size)
	le.PutUint32(b[8:], uint32(h.RemotePort))
	le.PutUint32(b[12:], uint32(h.LocalPort))
	le.PutUint32(b[16:], uint32(h.Voucher))
	le.PutUint32(b[20:], uint32(h.ID))
}

// ParseHeader reads a header from the first HeaderSize bytes of b.
func ParseHeader(b []byte) Header {
	le := binary.LittleEndian
	return Header{
		Bits:       le.Uint32(b[0:]),
		Size:       le.Uint32(b[4:]),
		RemotePort: Port(le.Uint32(b[8:])),
		LocalPort:  Port(le.Uint32(b[12:])),
		Voucher:    Port(le.Uint32(b[16:])),
		ID:         int32(le.Uint32(b[20:])),
	}
}

// MsgBits builds msgh_bits from remote and local dispositions.
func MsgBits(remote, local uint32) uint32 { return remote | local<<8 }
