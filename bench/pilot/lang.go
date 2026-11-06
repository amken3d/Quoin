package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Lang is one arm of the comparison: the spec text charged to every prompt
// and how to turn a source file into an ELF for the QEMU Cortex-M33.
type Lang struct {
	Name  string
	Ext   string
	Spec  string
	Build func(ctx context.Context, dir, src string) (elf, diag string, ok bool)
}

// inputAsm provides the input API to both arms. The harness loads
// [u32 length][bytes] at 0x10380000 with QEMU's generic loader.
const inputAsm = `	.syntax unified
	.thumb
	.equ QL_INPUT, 0x10380000
	.section .text.ql_input, "ax", %progbits
	.global input_len, input_byte
	.thumb_func
input_len:
	ldr r0, =QL_INPUT
	ldr r0, [r0]
	bx lr
	.thumb_func
input_byte:
	ldr r1, =QL_INPUT
	ldr r2, [r1]
	cmp r0, r2
	bhs.w ql_trap
	adds r1, #4
	ldrb r0, [r1, r0]
	bx lr
	.ltorg
`

// cStub supplies what freestanding GCC output may need: a vector table,
// memset/memcpy, and the data-stack symbols the shared runtime reports on.
const cStub = `	.syntax unified
	.thumb
	.section .vectors, "a"
	.align 7
	.word _stack_top
	.word _start + 1
	.rept 14
	.word ql_unhandled + 1
	.endr
	.global ql_dstack, ql_dstack_end, memset, memcpy, memmove
	.section .bss.qstub, "aw", %nobits
	.align 3
ql_dstack:
ql_dstack_end:
	.section .text.qstub, "ax", %progbits
	.thumb_func
memset:
	mov r3, r0
1:	cbz r2, 2f
	strb r1, [r3], #1
	subs r2, #1
	b 1b
2:	bx lr
	.thumb_func
memmove:
	cmp r0, r1
	bls memcpy
	add r1, r2
	add r3, r0, r2
5:	cbz r2, 6f
	ldrb r12, [r1, #-1]!
	strb r12, [r3, #-1]!
	subs r2, #1
	b 5b
6:	bx lr
	.thumb_func
memcpy:
	mov r3, r0
3:	cbz r2, 4f
	ldrb r12, [r1], #1
	strb r12, [r3], #1
	subs r2, #1
	b 3b
4:	bx lr
`

const quoinPlatform = `
# Platform

The program runs on an ARM Cortex-M33 (QEMU mps2-an505) and is built with
` + "`quoinc build --target m33-qemu`" + `. Copy these declarations into your program
exactly as written and use them for all input and output:

    extern fn emit(v: i32) stack 32 cycles 400    // print a signed number on its own line
    extern fn emitu(v: u32) stack 32 cycles 400   // print an unsigned number on its own line
    extern fn input_len() u32 stack 0 cycles 8    // number of input bytes
    extern fn input_byte(i: u32) u8 stack 0 cycles 16  // input byte i (0-based); traps if i >= input_len()

The entry point is ` + "`fn main()`" + `. The program passes when main returns normally (no trap)
and the printed lines match exactly. Reply with exactly one fenced code block (` + "```quoin" + `)
containing the complete program.
`

const cPlatform = `# Platform

The program runs on an ARM Cortex-M33 (QEMU mps2-an505), bare metal, with no C library and
no operating system. It is compiled with

    arm-none-eabi-gcc -std=c99 -O2 -Wall -mcpu=cortex-m33 -mthumb -ffreestanding -nostdlib

You may include <stdint.h>, <stdbool.h> and <stddef.h> only; there is no printf, malloc or
string library. These functions are provided; declare them yourself and use them for all
input and output:

    void emit(int32_t v);         // print a signed number on its own line
    void emitu(uint32_t v);       // print an unsigned number on its own line
    uint32_t input_len(void);     // number of input bytes
    uint8_t input_byte(uint32_t i);  // input byte i (0-based); traps if i >= input_len()

The entry point is ` + "`int main(void)`" + `. The program passes when main returns normally
(no trap or fault) and the printed lines match exactly. Reply with exactly one fenced code
block (` + "```c" + `) containing the complete program.
`

const systemPreamble = `You write firmware for a microcontroller. Each task describes what the program
must compute from its input and print. Write a correct, complete program for the platform and
language described below.

`

// toolchain holds paths shared by all builds.
type toolchain struct {
	quoinc  string
	runtime string // quoin_rt.s for m33-qemu
	ldfile  string // linker script for m33-qemu
	input   string // inputAsm on disk
	cstub   string
}

func setupToolchain(work, repo string) (*toolchain, error) {
	tc := &toolchain{quoinc: filepath.Join(work, "quoinc")}
	cmd := exec.Command("go", "build", "-o", tc.quoinc, "./cmd/quoinc")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building quoinc: %v\n%s", err, out)
	}
	// Generate the shared runtime and linker script from a trivial program.
	rt := filepath.Join(work, "rt")
	os.MkdirAll(rt, 0o755)
	stub := filepath.Join(rt, "stub.qn")
	os.WriteFile(stub, []byte("fn main()\n  return\n"), 0o644)
	if out, err := exec.Command(tc.quoinc, "build", "--target", "m33-qemu", "--no-link", "-o", rt, stub).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("generating runtime: %v\n%s", err, out)
	}
	tc.runtime = filepath.Join(rt, "quoin_rt.s")
	tc.ldfile = filepath.Join(rt, "stub.ld")
	tc.input = filepath.Join(rt, "input.s")
	tc.cstub = filepath.Join(rt, "cstub.s")
	os.WriteFile(tc.input, []byte(inputAsm), 0o644)
	os.WriteFile(tc.cstub, []byte(cStub), 0o644)
	return tc, nil
}

func run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (tc *toolchain) langs(repo string) (map[string]*Lang, error) {
	spec, err := os.ReadFile(filepath.Join(repo, "docs", "language.md"))
	if err != nil {
		return nil, err
	}
	quoin := &Lang{Name: "quoin", Ext: ".qn", Spec: systemPreamble + string(spec) + "\n" + quoinPlatform}
	quoin.Build = func(ctx context.Context, dir, src string) (string, string, bool) {
		os.WriteFile(filepath.Join(dir, "prog.qn"), []byte(src), 0o644)
		out, err := run(ctx, dir, tc.quoinc, "build", "--target", "m33-qemu", "--no-link", "-o", ".", "prog.qn")
		if err != nil {
			return "", stripSummary(out), false
		}
		objs := []string{}
		for _, s := range []string{filepath.Join(dir, "prog.s"), filepath.Join(dir, "quoin_rt.s"), tc.input} {
			o := s + ".o"
			if out, err := run(ctx, dir, "arm-none-eabi-as", "-mcpu=cortex-m33", "-mthumb", "-o", o, s); err != nil {
				return "", "internal assembler error (not the program's fault):\n" + out, false
			}
			objs = append(objs, o)
		}
		elf := filepath.Join(dir, "prog.elf")
		args := append([]string{"-T", filepath.Join(dir, "prog.ld"), "-nostdlib", "--gc-sections", "-o", elf}, objs...)
		if out, err := run(ctx, dir, "arm-none-eabi-ld", args...); err != nil {
			return "", "link failed:\n" + out, false
		}
		return elf, "", true
	}
	c := &Lang{Name: "c", Ext: ".c", Spec: systemPreamble + cPlatform}
	c.Build = func(ctx context.Context, dir, src string) (string, string, bool) {
		os.WriteFile(filepath.Join(dir, "prog.c"), []byte(src), 0o644)
		elf := filepath.Join(dir, "prog.elf")
		out, err := run(ctx, dir, "arm-none-eabi-gcc", "-std=c99", "-O2", "-Wall", "-mcpu=cortex-m33", "-mthumb",
			"-ffreestanding", "-nostdlib", "-nostartfiles", "-T", tc.ldfile, "-o", elf,
			"prog.c", tc.runtime, tc.input, tc.cstub, "-Wl,--gc-sections", "-lgcc")
		if err != nil {
			return "", out, false
		}
		return elf, "", true
	}
	return map[string]*Lang{"quoin": quoin, "c": c}, nil
}

// stripSummary drops quoinc's STACK/CYCLES/CHECKS lines from failure output
// (on failure they are absent anyway) and keeps diagnostics verbatim.
func stripSummary(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "STACK ") || strings.HasPrefix(l, "CYCLES ") || strings.HasPrefix(l, "CHECKS ") || strings.HasPrefix(l, "WROTE ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

var fenceRe = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)[ \t]*\n(.*?)```")

// extractCode returns the last fenced code block in a model reply.
func extractCode(text string) string {
	m := fenceRe.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][2]
}
