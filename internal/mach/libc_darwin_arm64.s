#include "textflag.h"

// Trampolines for the libSystem functions in libc_darwin_arm64.go. The runtime
// calls them on the system stack with the C calling convention.

TEXT libc_mach_msg_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_msg(SB)
GLOBL	·libc_mach_msg_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_msg_trampoline_addr(SB)/8, $libc_mach_msg_trampoline<>(SB)

TEXT libc_mach_msg_destroy_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_msg_destroy(SB)
GLOBL	·libc_mach_msg_destroy_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_msg_destroy_trampoline_addr(SB)/8, $libc_mach_msg_destroy_trampoline<>(SB)

TEXT libc_mach_port_allocate_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_port_allocate(SB)
GLOBL	·libc_mach_port_allocate_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_port_allocate_trampoline_addr(SB)/8, $libc_mach_port_allocate_trampoline<>(SB)

TEXT libc_mach_port_insert_right_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_port_insert_right(SB)
GLOBL	·libc_mach_port_insert_right_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_port_insert_right_trampoline_addr(SB)/8, $libc_mach_port_insert_right_trampoline<>(SB)

TEXT libc_mach_port_deallocate_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_port_deallocate(SB)
GLOBL	·libc_mach_port_deallocate_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_port_deallocate_trampoline_addr(SB)/8, $libc_mach_port_deallocate_trampoline<>(SB)

TEXT libc_mach_port_mod_refs_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_port_mod_refs(SB)
GLOBL	·libc_mach_port_mod_refs_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_port_mod_refs_trampoline_addr(SB)/8, $libc_mach_port_mod_refs_trampoline<>(SB)

TEXT libc_mach_port_request_notification_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_port_request_notification(SB)
GLOBL	·libc_mach_port_request_notification_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_port_request_notification_trampoline_addr(SB)/8, $libc_mach_port_request_notification_trampoline<>(SB)

TEXT libc_bootstrap_look_up_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_bootstrap_look_up(SB)
GLOBL	·libc_bootstrap_look_up_trampoline_addr(SB), RODATA, $8
DATA	·libc_bootstrap_look_up_trampoline_addr(SB)/8, $libc_bootstrap_look_up_trampoline<>(SB)

TEXT libc_bootstrap_check_in_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_bootstrap_check_in(SB)
GLOBL	·libc_bootstrap_check_in_trampoline_addr(SB), RODATA, $8
DATA	·libc_bootstrap_check_in_trampoline_addr(SB)/8, $libc_bootstrap_check_in_trampoline<>(SB)

TEXT libc_vm_allocate_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_vm_allocate(SB)
GLOBL	·libc_vm_allocate_trampoline_addr(SB), RODATA, $8
DATA	·libc_vm_allocate_trampoline_addr(SB)/8, $libc_vm_allocate_trampoline<>(SB)

TEXT libc_vm_deallocate_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_vm_deallocate(SB)
GLOBL	·libc_vm_deallocate_trampoline_addr(SB), RODATA, $8
DATA	·libc_vm_deallocate_trampoline_addr(SB)/8, $libc_vm_deallocate_trampoline<>(SB)

TEXT libc_mach_make_memory_entry_64_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_make_memory_entry_64(SB)
GLOBL	·libc_mach_make_memory_entry_64_trampoline_addr(SB), RODATA, $8
DATA	·libc_mach_make_memory_entry_64_trampoline_addr(SB)/8, $libc_mach_make_memory_entry_64_trampoline<>(SB)

TEXT libc_fileport_makeport_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_fileport_makeport(SB)
GLOBL	·libc_fileport_makeport_trampoline_addr(SB), RODATA, $8
DATA	·libc_fileport_makeport_trampoline_addr(SB)/8, $libc_fileport_makeport_trampoline<>(SB)

TEXT libc_fileport_makefd_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_fileport_makefd(SB)
GLOBL	·libc_fileport_makefd_trampoline_addr(SB), RODATA, $8
DATA	·libc_fileport_makefd_trampoline_addr(SB)/8, $libc_fileport_makefd_trampoline<>(SB)

// machVMMapShim calls mach_vm_map. R0 points to a vmMapFrame. The last three
// arguments go on the stack, packed at 4-byte alignment as the Apple arm64 ABI
// requires. The frame pointer is saved at 16(RSP) for the return value.
TEXT machVMMapShim<>(SB),NOSPLIT,$0
	SUB	$32, RSP
	MOVD	R0, 16(RSP)
	MOVW	64(R0), R8	// cur_protection
	MOVW	R8, 0(RSP)
	MOVW	68(R0), R8	// max_protection
	MOVW	R8, 4(RSP)
	MOVW	72(R0), R8	// inheritance
	MOVW	R8, 8(RSP)
	MOVD	0(R0), R9	// target_task
	ADD	$8, R0, R1	// &address
	MOVD	16(R0), R2	// size
	MOVD	24(R0), R3	// mask
	MOVD	32(R0), R4	// flags
	MOVD	40(R0), R5	// object
	MOVD	48(R0), R6	// offset
	MOVD	56(R0), R7	// copy
	MOVD	R9, R0
	BL	libc_mach_vm_map(SB)
	MOVD	16(RSP), R2
	MOVW	R0, 76(R2)	// ret
	ADD	$32, RSP
	RET
GLOBL	·machVMMapShimAddr(SB), RODATA, $8
DATA	·machVMMapShimAddr(SB)/8, $machVMMapShim<>(SB)

// func loadTaskSelf() uint32
TEXT ·loadTaskSelf(SB),NOSPLIT,$0-4
	MOVD	$libc_mach_task_self_(SB), R0
	MOVWU	(R0), R0
	MOVW	R0, ret+0(FP)
	RET

// func loadBootstrapPort() uint32
TEXT ·loadBootstrapPort(SB),NOSPLIT,$0-4
	MOVD	$libc_bootstrap_port(SB), R0
	MOVWU	(R0), R0
	MOVW	R0, ret+0(FP)
	RET
