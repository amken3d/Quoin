package thumb

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/amken3d/quoin/internal/check"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

// Target is a Thumb-2 platform: how the vector table is laid out, what the
// runtime does around main, and how the image is linked.
type Target struct {
	Name    string
	Core    []string // core exception slots 1..15 by handler name
	IRQs    []string // external interrupt names, index = IRQ number
	Runtime string   // startup and support assembly
	Linker  string   // GNU ld script
	Doc     string
}

// Targets lists the native targets by name.
var Targets = map[string]*Target{"rp2350": RP2350, "m33-qemu": M33QEMU}

var coreSlots = []string{"", "", "NMI", "HardFault", "MemManage", "BusFault", "UsageFault", "SecureFault",
	"", "", "", "SVCall", "DebugMon", "", "PendSV", "SysTick"}

// rp2350IRQs follows pico-sdk's hardware/regs/intctrl.h for RP2350.
var rp2350IRQs = []string{
	"TIMER0_IRQ_0", "TIMER0_IRQ_1", "TIMER0_IRQ_2", "TIMER0_IRQ_3",
	"TIMER1_IRQ_0", "TIMER1_IRQ_1", "TIMER1_IRQ_2", "TIMER1_IRQ_3",
	"PWM_IRQ_WRAP_0", "PWM_IRQ_WRAP_1", "DMA_IRQ_0", "DMA_IRQ_1", "DMA_IRQ_2", "DMA_IRQ_3",
	"USBCTRL_IRQ", "PIO0_IRQ_0", "PIO0_IRQ_1", "PIO1_IRQ_0", "PIO1_IRQ_1", "PIO2_IRQ_0", "PIO2_IRQ_1",
	"IO_IRQ_BANK0", "IO_IRQ_BANK0_NS", "IO_IRQ_QSPI", "IO_IRQ_QSPI_NS",
	"SIO_IRQ_FIFO", "SIO_IRQ_BELL", "SIO_IRQ_FIFO_NS", "SIO_IRQ_BELL_NS", "SIO_IRQ_MTIMECMP",
	"CLOCKS_IRQ", "SPI0_IRQ", "SPI1_IRQ", "UART0_IRQ", "UART1_IRQ", "ADC_IRQ_FIFO", "I2C0_IRQ", "I2C1_IRQ",
	"OTP_IRQ", "TRNG_IRQ", "PROC0_IRQ_CTI", "PROC1_IRQ_CTI", "PLL_SYS_IRQ", "PLL_USB_IRQ",
	"POWMAN_IRQ_POW", "POWMAN_IRQ_TIMER", "SPARE_IRQ_0", "SPARE_IRQ_1", "SPARE_IRQ_2", "SPARE_IRQ_3",
	"SPARE_IRQ_4", "SPARE_IRQ_5",
}

// Vectors builds the vector table for a program, placing each isr by name.
func (t *Target) Vectors(p *check.Program, d *diag.List) string {
	slots := map[string]int{}
	for i, n := range t.Core {
		if n != "" {
			slots[n] = i
		}
	}
	for i, n := range t.IRQs {
		slots[n] = 16 + i
	}
	table := make([]string, 16+len(t.IRQs))
	for i := range table {
		switch {
		case i == 0:
			table[i] = "_stack_top"
		case i == 1:
			table[i] = "_start + 1"
		case i < 16 && t.Core[i] == "":
			table[i] = "0"
		default:
			table[i] = "ql_unhandled + 1"
		}
	}
	for _, fn := range p.Entries {
		if !fn.ISR {
			continue
		}
		slot, ok := slots[fn.Name]
		if !ok {
			var names []string
			for n := range slots {
				names = append(names, n)
			}
			sort.Strings(names)
			d.ErrorFix(fn.Pos, "E208", fmt.Sprintf("isr %s is not an interrupt of %s", fn.Name, t.Name),
				"name it after its vector: "+strings.Join(shorten(names, 12), ", ")+", ...")
			continue
		}
		table[slot] = fn.CName + " + 1"
	}
	var b strings.Builder
	b.WriteString("\n\t.section .vectors, \"a\"\n\t.align 7\n\t.global _vectors\n_vectors:\n")
	for i, e := range table {
		fmt.Fprintf(&b, "\t.word %s\t@ %d\n", e, i)
	}
	return b.String()
}

func shorten(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// IRQEntry returns the exception-entry latency to add to an isr bound.
func IRQEntry(t Timing, fn *types.Fn) int64 {
	if fn.ISR {
		return t.ExcEntry
	}
	return 0
}

const semihostSupport = `
@ ---- semihosting support (QEMU) ------------------------------------------
	.section .text.ql_support, "ax", %progbits
	.align 2
	.equ SYS_WRITE0, 0x04
	.equ SYS_EXIT_EXTENDED, 0x20
	.equ ADP_Stopped_ApplicationExit, 0x20026

@ emit(v: i32) and emitu(v: u32): print a decimal line. Stack: 32 bytes.
	.global emit, emitu
	.type emit, %function
	.thumb_func
emit:
	push {r4, r5, lr}
	movs r5, #0
	cmp r0, #0
	bge 1f
	movs r5, #1
	rsbs r0, r0, #0
	b 1f
	.type emitu, %function
	.thumb_func
emitu:
	push {r4, r5, lr}
	movs r5, #0
1:	sub sp, sp, #20
	add r1, sp, #19
	movs r2, #0
	strb r2, [r1]
	subs r1, #1
	movs r2, #10
	strb r2, [r1]
	movs r3, #10
2:	udiv r2, r0, r3
	mls r4, r2, r3, r0
	adds r4, #48
	subs r1, #1
	strb r4, [r1]
	movs r0, r2
	cmp r0, #0
	bne 2b
	cbz r5, 3f
	movs r4, #45
	subs r1, #1
	strb r4, [r1]
3:	movs r0, #SYS_WRITE0
	bkpt 0xab
	add sp, sp, #20
	pop {r4, r5, pc}

@ ql_write(msg): print a NUL-terminated string. No stack use.
	.thumb_func
ql_write:
	mov r1, r0
	movs r0, #SYS_WRITE0
	bkpt 0xab
	bx lr

@ ql_exit(code): terminate QEMU with an exit status.
	.global ql_exit
	.thumb_func
ql_exit:
	ldr r1, =ql_exit_block
	str r0, [r1, #4]
	movs r0, #SYS_EXIT_EXTENDED
	bkpt 0xab
4:	b 4b

@ ql_trap: a runtime check failed. Prints "trap" and exits 70; uses no stack
@ so it never disturbs the measured high-water mark.
	.global ql_trap
	.thumb_func
ql_trap:
	ldr r0, =ql_msg_trap
	mov r1, r0
	movs r0, #SYS_WRITE0
	bkpt 0xab
	movs r0, #70
	b ql_exit

	.global ql_unhandled
	.thumb_func
ql_unhandled:
	ldr r1, =ql_msg_fault
	movs r0, #SYS_WRITE0
	bkpt 0xab
	movs r0, #71
	b ql_exit

@ After main returns: report stack and data-stack high-water marks.
	.thumb_func
ql_report:
	push {r4, lr}
	ldr r0, =__stack_limit
	ldr r2, =0xDEADBEEF
5:	ldr r1, [r0]
	cmp r1, r2
	bne 6f
	adds r0, #4
	b 5b
6:	ldr r4, =_stack_top
	subs r4, r4, r0
	ldr r0, =ql_msg_stack
	bl ql_write
	mov r0, r4
	bl emitu
	ldr r0, =ql_dstack_end
	ldr r1, =ql_dstack
	ldr r2, =0xDEADBEEF
7:	cmp r0, r1
	beq 8f
	ldr r3, [r0, #-4]
	cmp r3, r2
	bne 8f
	subs r0, #4
	b 7b
8:	subs r4, r0, r1
	ldr r0, =ql_msg_dstack
	bl ql_write
	mov r0, r4
	bl emitu
	pop {r4, pc}
	.ltorg

	.section .rodata.ql_support, "a"
ql_msg_trap:	.asciz "trap\n"
ql_msg_fault:	.asciz "fault\n"
ql_msg_stack:	.asciz "STACKHW "
ql_msg_dstack:	.asciz "DSTACKHW "
	.section .data.ql_support, "aw"
	.align 2
ql_exit_block:	.word ADP_Stopped_ApplicationExit, 0
`

// M33QEMU is QEMU's mps2-an505 board (Cortex-M33), used for verification.
// The runtime paints the stack and data stack, runs main, then prints the
// measured high-water marks before exiting through semihosting.
var M33QEMU = &Target{
	Name: "m33-qemu",
	Core: coreSlots,
	Doc:  "QEMU mps2-an505 (Cortex-M33), semihosting I/O",
	Runtime: `	.syntax unified
	.cpu cortex-m33
	.thumb
	.section .text._start, "ax", %progbits
	.align 2
	.global _start
	.type _start, %function
	.thumb_func
_start:
	ldr r0, =__bss_start__
	ldr r1, =__bss_end__
	movs r2, #0
1:	cmp r0, r1
	bhs 2f
	str r2, [r0], #4
	b 1b
2:	ldr r2, =0xDEADBEEF
	ldr r0, =ql_dstack
	ldr r1, =ql_dstack_end
3:	cmp r0, r1
	bhs 4f
	str r2, [r0], #4
	b 3b
4:	ldr r0, =__stack_limit
	mov r1, sp
	subs r1, #8
5:	cmp r0, r1
	bhs 6f
	str r2, [r0], #4
	b 5b
6:	bl main
	.global ql_after_main
ql_after_main:
	bl ql_report
	movs r0, #0
	b ql_exit
	.ltorg
` + semihostSupport,
	Linker: `/* Generated by quoinc: QEMU mps2-an505 (Cortex-M33), everything in SSRAM1. */
ENTRY(_start)
MEMORY { SSRAM1 (rwx) : ORIGIN = 0x10000000, LENGTH = 4M }
SECTIONS
{
    .text : ALIGN(128) { KEEP(*(.vectors)) *(.text*) *(.rodata*) . = ALIGN(4); } > SSRAM1
    .data : ALIGN(4) { *(.data*) . = ALIGN(4); } > SSRAM1
    .bss (NOLOAD) : ALIGN(8) { __bss_start__ = .; *(.bss*) *(COMMON) . = ALIGN(8); __bss_end__ = .; } > SSRAM1
    .stack (NOLOAD) : ALIGN(8) { __stack_limit = .; . = . + 0x4000; _stack_top = .; } > SSRAM1
    /DISCARD/ : { *(.ARM.exidx*) *(.ARM.attributes) *(.comment) }
}
`,
}

// RP2350 is the Raspberry Pi RP2350 (Cortex-M33, secure), as an
// SRAM-resident image loaded by the bootrom from UF2. Code runs from
// zero-wait SRAM, which the cycle model assumes.
var RP2350 = &Target{
	Name: "rp2350",
	Core: coreSlots,
	IRQs: rp2350IRQs,
	Doc:  "RP2350 Cortex-M33 secure, SRAM-resident image (UF2 via BOOTSEL)",
	Runtime: `	.syntax unified
	.cpu cortex-m33
	.thumb

@ IMAGE_DEF block (picobin): EXE, secure, Arm, RP2350 (flags 0x1021).
	.section .image_def, "a"
	.align 2
ql_image_def:
	.word 0xffffded3
	.word 0x42 | (1 << 8) | (0x1021 << 16)
	.word 0xff | (1 << 8)
	.word 0
	.word 0xab123579

	.section .text._start, "ax", %progbits
	.align 2
	.global _start
	.type _start, %function
	.thumb_func
_start:
	@ The bootrom enters SRAM images without MSPLIM cleared and possibly
	@ without the RCP enabled (as in pico-sdk crt0 and ticktrace startup).
	ldr r0, =0xE000ED88
	movs r1, #0xC0
	lsls r1, r1, #8
	str r1, [r0]
	mrc p7, #1, APSR_nzcv, c0, c0, #0
	bmi 1f
	mcrr p7, #8, r0, r0, c0
	mcrr p7, #8, r0, r0, c1
	sev
1:	movs r0, #0
	msr msplim, r0
	ldr r0, =0xE000ED08
	ldr r1, =_vectors
	str r1, [r0]
	ldr r0, =__bss_start__
	ldr r1, =__bss_end__
	movs r2, #0
2:	cmp r0, r1
	bhs 3f
	str r2, [r0], #4
	b 2b
3:	bl main
4:	wfi
	b 4b
	.ltorg

@ ql_trap: a runtime check failed. Halt at a breakpoint for the debugger.
	.global ql_trap
	.thumb_func
ql_trap:
	bkpt #0
5:	b 5b

	.global ql_unhandled
	.thumb_func
ql_unhandled:
	bkpt #1
6:	b 6b
`,
	Linker: `/* Generated by quoinc: RP2350 SRAM-resident image (load via UF2). */
ENTRY(_start)
MEMORY { SRAM (rwx) : ORIGIN = 0x20000000, LENGTH = 512K }
SECTIONS
{
    .text 0x20000000 : ALIGN(4)
    {
        KEEP(*(.vectors))
        . = ALIGN(4);
        KEEP(*(.image_def))
        *(.text*) *(.rodata*)
        . = ALIGN(4);
    } > SRAM
    .data : ALIGN(4) { *(.data*) . = ALIGN(4); } > SRAM
    .bss (NOLOAD) : ALIGN(8) { __bss_start__ = .; *(.bss*) *(COMMON) . = ALIGN(8); __bss_end__ = .; } > SRAM
    _stack_top = ORIGIN(SRAM) + LENGTH(SRAM);
    __stack_limit = __bss_end__;
    ASSERT(__quoin_call_stack_bytes <= _stack_top - __bss_end__, "quoin: proven call stack does not fit in SRAM")
    /DISCARD/ : { *(.ARM.exidx*) *(.ARM.attributes) *(.comment) }
}
`,
}

// RP2350 UF2 family ID for Arm secure executables.
const uf2FamilyRP2350ArmS = 0xe48bff59

// UF2 wraps a flat binary loaded at base into UF2 blocks.
func UF2(bin []byte, base uint32) []byte {
	const payload = 256
	n := (len(bin) + payload - 1) / payload
	out := make([]byte, 0, n*512)
	for i := 0; i < n; i++ {
		blk := make([]byte, 512)
		le := binary.LittleEndian
		le.PutUint32(blk[0:], 0x0A324655)
		le.PutUint32(blk[4:], 0x9E5D5157)
		le.PutUint32(blk[8:], 0x00002000) // family ID present
		le.PutUint32(blk[12:], base+uint32(i*payload))
		le.PutUint32(blk[16:], payload)
		le.PutUint32(blk[20:], uint32(i))
		le.PutUint32(blk[24:], uint32(n))
		le.PutUint32(blk[28:], uf2FamilyRP2350ArmS)
		end := min((i+1)*payload, len(bin))
		copy(blk[32:], bin[i*payload:end])
		le.PutUint32(blk[508:], 0x0AB16F30)
		out = append(out, blk...)
	}
	return out
}
