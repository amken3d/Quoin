package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Verdict is the result of running a program on one input.
type Verdict struct {
	Pass     bool
	Kind     string // pass, wrong, trap, timeout, stack, fault
	Feedback string
	StackHW  int64
}

// runQEMU runs elf on input and returns its output lines, exit code and
// measured stack high-water mark.
func runQEMU(ctx context.Context, dir, elf string, input []byte, timeout time.Duration) ([]string, int, int64, bool, error) {
	blob := make([]byte, 4+len(input))
	binary.LittleEndian.PutUint32(blob, uint32(len(input)))
	copy(blob[4:], input)
	in := filepath.Join(dir, "input.bin")
	if err := os.WriteFile(in, blob, 0o644); err != nil {
		return nil, -1, -1, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "qemu-system-arm", "-M", "mps2-an505", "-cpu", "cortex-m33", "-nographic",
		"-monitor", "none", "-semihosting-config", "enable=on,target=native", "-no-reboot",
		"-kernel", elf, "-device", "loader,file="+in+",addr=0x10380000,force-raw=on")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, -1, -1, true, nil
	}
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return nil, -1, -1, false, err
		}
	}
	var lines []string
	var hw int64 = -1
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "STACKHW "):
			hw, _ = strconv.ParseInt(strings.TrimPrefix(l, "STACKHW "), 10, 64)
		case strings.HasPrefix(l, "DSTACKHW "), l == "":
		default:
			lines = append(lines, l)
		}
	}
	return lines, code, hw, false, nil
}

func hexInput(in []byte) string {
	const max = 96
	var b strings.Builder
	for i, c := range in {
		if i == max {
			fmt.Fprintf(&b, " ... (%d bytes total)", len(in))
			break
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02x", c)
	}
	if len(in) == 0 {
		return "(empty)"
	}
	return b.String()
}

func clip(lines []string, n int) string {
	if len(lines) > n {
		return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n... (%d lines total)", len(lines))
	}
	if len(lines) == 0 {
		return "(no output)"
	}
	return strings.Join(lines, "\n")
}

// judge runs the program on one input and compares it with the reference.
func judge(ctx context.Context, dir, elf string, t *Task, input []byte) (Verdict, error) {
	got, code, hw, timedOut, err := runQEMU(ctx, dir, elf, input, 10*time.Second)
	if err != nil {
		return Verdict{}, err
	}
	want := t.Ref(input)
	in := "Input bytes (hex): " + hexInput(input)
	switch {
	case timedOut:
		return Verdict{Kind: "timeout", Feedback: "The program did not finish within 10 seconds (probably an endless loop).\n" + in}, nil
	case len(got) > 0 && got[len(got)-1] == "trap":
		return Verdict{Kind: "trap", Feedback: "The program trapped: a runtime check failed or input_byte was called past the end of the input.\n" +
			in + "\nOutput before the trap:\n" + clip(got[:len(got)-1], 40)}, nil
	case code != 0:
		return Verdict{Kind: "fault", Feedback: fmt.Sprintf("The program faulted (exit code %d).\n%s\nOutput:\n%s", code, in, clip(got, 40))}, nil
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		return Verdict{Kind: "wrong", StackHW: hw, Feedback: "Wrong output.\n" + in + "\nExpected output:\n" + clip(want, 40) +
			"\nYour output:\n" + clip(got, 40)}, nil
	}
	if hw > t.StackBound {
		return Verdict{Kind: "stack", StackHW: hw, Feedback: fmt.Sprintf("Correct output, but the measured stack use was %d bytes; the budget is %d bytes.",
			hw, t.StackBound)}, nil
	}
	return Verdict{Pass: true, Kind: "pass", StackHW: hw}, nil
}
