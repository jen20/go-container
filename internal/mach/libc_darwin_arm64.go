package mach

import (
	"syscall"
	"unsafe"
)

// This file binds the libSystem functions that the package uses. It uses the
// same method as the syscall package: a cgo_import_dynamic directive makes
// the Go linker record the import, an assembly trampoline jumps to the symbol,
// and the runtime's libc call path (syscall.syscall6 and syscall.syscall9)
// calls the trampoline on the system stack. Cross-compilation works.
//
// The syscall helpers set err only when r1 is -1. Mach functions return a
// kern_return_t in r1, so callers ignore err.

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:linkname syscall_syscall9 syscall.syscall9
func syscall_syscall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:cgo_import_dynamic libc_mach_task_self_ mach_task_self_ "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_bootstrap_port bootstrap_port "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_msg mach_msg "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_msg_destroy mach_msg_destroy "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_allocate mach_port_allocate "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_insert_right mach_port_insert_right "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_deallocate mach_port_deallocate "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_mod_refs mach_port_mod_refs "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_request_notification mach_port_request_notification "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_bootstrap_look_up bootstrap_look_up "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_bootstrap_check_in bootstrap_check_in "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_vm_allocate vm_allocate "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_vm_deallocate vm_deallocate "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_vm_map mach_vm_map "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_make_memory_entry_64 mach_make_memory_entry_64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_fileport_makeport fileport_makeport "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_fileport_makefd fileport_makefd "/usr/lib/libSystem.B.dylib"

// Trampoline addresses, set in libc_darwin_arm64.s.
var (
	libc_mach_msg_trampoline_addr                       uintptr
	libc_mach_msg_destroy_trampoline_addr               uintptr
	libc_mach_port_allocate_trampoline_addr             uintptr
	libc_mach_port_insert_right_trampoline_addr         uintptr
	libc_mach_port_deallocate_trampoline_addr           uintptr
	libc_mach_port_mod_refs_trampoline_addr             uintptr
	libc_mach_port_request_notification_trampoline_addr uintptr
	libc_bootstrap_look_up_trampoline_addr              uintptr
	libc_bootstrap_check_in_trampoline_addr             uintptr
	libc_vm_allocate_trampoline_addr                    uintptr
	libc_vm_deallocate_trampoline_addr                  uintptr
	libc_mach_make_memory_entry_64_trampoline_addr      uintptr
	libc_fileport_makeport_trampoline_addr              uintptr
	libc_fileport_makefd_trampoline_addr                uintptr
	machVMMapShimAddr                                   uintptr
)

// loadTaskSelf and loadBootstrapPort read libSystem data symbols.
func loadTaskSelf() uint32
func loadBootstrapPort() uint32

func machMsg(msg unsafe.Pointer, option int32, sendSize, rcvSize uint32, rcvName Port, timeout uint32, notify Port) int32 {
	r, _, _ := syscall_syscall9(libc_mach_msg_trampoline_addr,
		uintptr(msg), uintptr(option), uintptr(sendSize), uintptr(rcvSize),
		uintptr(rcvName), uintptr(timeout), uintptr(notify), 0, 0)
	return int32(r)
}

func machMsgDestroy(msg unsafe.Pointer) {
	_, _, _ = syscall_syscall6(libc_mach_msg_destroy_trampoline_addr, uintptr(msg), 0, 0, 0, 0, 0)
}

func portAllocate(task Port, right uint32, name *Port) int32 {
	r, _, _ := syscall_syscall6(libc_mach_port_allocate_trampoline_addr,
		uintptr(task), uintptr(right), uintptr(unsafe.Pointer(name)), 0, 0, 0)
	return int32(r)
}

func portInsertRight(task, name, poly Port, polyPoly uint32) int32 {
	r, _, _ := syscall_syscall6(libc_mach_port_insert_right_trampoline_addr,
		uintptr(task), uintptr(name), uintptr(poly), uintptr(polyPoly), 0, 0)
	return int32(r)
}

func portDeallocate(task, name Port) int32 {
	r, _, _ := syscall_syscall6(libc_mach_port_deallocate_trampoline_addr,
		uintptr(task), uintptr(name), 0, 0, 0, 0)
	return int32(r)
}

func portModRefs(task, name Port, right uint32, delta int32) int32 {
	r, _, _ := syscall_syscall6(libc_mach_port_mod_refs_trampoline_addr,
		uintptr(task), uintptr(name), uintptr(right), uintptr(delta), 0, 0)
	return int32(r)
}

func requestNotify(task, name Port, variant int32, sync uint32, notify Port, notifyPoly uint32, previous *Port) int32 {
	r, _, _ := syscall_syscall9(libc_mach_port_request_notification_trampoline_addr,
		uintptr(task), uintptr(name), uintptr(variant), uintptr(sync),
		uintptr(notify), uintptr(notifyPoly), uintptr(unsafe.Pointer(previous)), 0, 0)
	return int32(r)
}

func bootstrapCall(fn uintptr, bp Port, name string, sp *Port) int32 {
	cname := append([]byte(name), 0)
	r, _, _ := syscall_syscall6(fn,
		uintptr(bp), uintptr(unsafe.Pointer(&cname[0])), uintptr(unsafe.Pointer(sp)), 0, 0, 0)
	return int32(r)
}

func bootstrapLookUp(bp Port, name string, sp *Port) int32 {
	return bootstrapCall(libc_bootstrap_look_up_trampoline_addr, bp, name, sp)
}

func bootstrapCheckIn(bp Port, name string, sp *Port) int32 {
	return bootstrapCall(libc_bootstrap_check_in_trampoline_addr, bp, name, sp)
}

func vmAllocate(task Port, addr *uintptr, size uintptr, flags int32) int32 {
	r, _, _ := syscall_syscall6(libc_vm_allocate_trampoline_addr,
		uintptr(task), uintptr(unsafe.Pointer(addr)), size, uintptr(flags), 0, 0)
	return int32(r)
}

func vmDeallocate(task Port, addr, size uintptr) int32 {
	r, _, _ := syscall_syscall6(libc_vm_deallocate_trampoline_addr,
		uintptr(task), addr, size, 0, 0, 0)
	return int32(r)
}

func makeMemoryEntry64(task Port, size *uint64, offset uint64, perm int32, handle *Port, parent Port) int32 {
	r, _, _ := syscall_syscall6(libc_mach_make_memory_entry_64_trampoline_addr,
		uintptr(task), uintptr(unsafe.Pointer(size)), uintptr(offset), uintptr(perm),
		uintptr(unsafe.Pointer(handle)), uintptr(parent))
	return int32(r)
}

func fileportMakeport(fd int32, port *Port) int32 {
	r, _, _ := syscall_syscall6(libc_fileport_makeport_trampoline_addr,
		uintptr(fd), uintptr(unsafe.Pointer(port)), 0, 0, 0, 0)
	return int32(r)
}

func fileportMakefd(port Port) int32 {
	r, _, _ := syscall_syscall6(libc_fileport_makefd_trampoline_addr,
		uintptr(port), 0, 0, 0, 0, 0)
	return int32(r)
}

// vmMapFrame holds the arguments of mach_vm_map for machVMMapShim. The
// layout must match libc_darwin_arm64.s. mach_vm_map takes 11 arguments, so
// three go on the stack, where the Apple arm64 ABI packs them at their natural
// 4-byte alignment. The runtime libc call path puts each stack argument in an
// 8-byte slot, so the shim lays out the stack itself.
type vmMapFrame struct {
	task    uint64 // 0
	addr    uint64 // 8, in and out; the shim passes its address
	size    uint64 // 16
	mask    uint64 // 24
	flags   uint64 // 32
	object  uint64 // 40
	offset  uint64 // 48
	copy    uint64 // 56
	cur     int32  // 64
	max     int32  // 68
	inherit uint32 // 72
	ret     int32  // 76
}

func machVMMap(task Port, addr *uint64, size, mask uint64, flags int32, object Port, offset uint64, copy int32, cur, max int32, inherit uint32) int32 {
	f := &vmMapFrame{
		task: uint64(task), addr: *addr, size: size, mask: mask,
		flags: uint64(uint32(flags)), object: uint64(object), offset: offset,
		copy: uint64(uint32(copy)), cur: cur, max: max, inherit: inherit,
	}
	// The shim stores the kern_return_t in f.ret, so the results of the
	// call itself carry nothing.
	_, _, _ = syscall_syscall6(machVMMapShimAddr, uintptr(unsafe.Pointer(f)), 0, 0, 0, 0, 0)
	*addr = f.addr
	return f.ret
}
