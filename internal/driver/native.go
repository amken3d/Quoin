package driver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amken3d/quoin/internal/stack"
	"github.com/amken3d/quoin/internal/thumb"
	"github.com/amken3d/quoin/internal/types"
)

// native runs the Thumb-2 backend after a successful check.
func (r *Result) native(tgt *thumb.Target, opt Options) {
	res := thumb.Generate(r.Program, thumb.Options{Target: tgt}, &r.Diags)
	if r.Diags.HasErrors() {
		return
	}
	su := map[string]int64{"ql_trap": 0}
	for k, v := range res.Frames {
		su[k] = v
	}
	r.Report = stack.Analyze(r.Program, stack.Options{Budget: opt.Budget, SU: su, ExcFrame: opt.ExcFrame, Source: "exact"}, &r.Diags)
	r.Bounds = res.Bounds
	r.Target = tgt
	r.checkClaims(true)
	if r.Diags.HasErrors() || !r.Report.Valid {
		return
	}
	var b strings.Builder
	b.WriteString(res.Asm)
	b.WriteString(thumb.DataStack(r.Report.DataTotal))
	b.WriteString(tgt.Vectors(r.Program, &r.Diags))
	fmt.Fprintf(&b, "\n\t.global __quoin_call_stack_bytes, __quoin_data_stack_bytes\n\t.set __quoin_call_stack_bytes, %d\n\t.set __quoin_data_stack_bytes, %d\n",
		r.Report.CallTotal, r.Report.DataTotal)
	r.Asm = b.String()
}

// checkClaims proves `where stack <= N` and `where cycles <= N`.
func (r *Result) checkClaims(native bool) {
	for _, fn := range r.Program.Fns {
		if fn.ClaimStack > 0 {
			got, ok := r.Report.FnCall[fn]
			switch {
			case !ok:
			case r.Report.Source == "est":
				r.Diags.Warn(fn.ClaimPos, "W301", fmt.Sprintf("stack claim on %s not proven: call frames are estimates", fn.Name),
					"use a native target, or quoinc stack --su")
			case got > fn.ClaimStack:
				r.Diags.ErrorFix(fn.ClaimPos, "E302", fmt.Sprintf("%s needs %dB of stack, claim is %dB", fn.Name, got, fn.ClaimStack),
					fmt.Sprintf("raise the claim to `stack <= %d`, or shrink the deepest call chain", got))
			}
		}
		if fn.ClaimCycles > 0 {
			if !native {
				r.Diags.Warn(fn.ClaimPos, "W301", "cycle claim on "+fn.Name+" not checked on the C target", "build with --target rp2350")
				continue
			}
			b, ok := r.Bounds[fn]
			switch {
			case !ok:
			case b.Unbounded:
				r.Diags.ErrorFix(fn.ClaimPos, "E303", fmt.Sprintf("%s has no cycle bound: %s at %s", fn.Name, b.Why, b.Pos),
					"bound the loop: `for i in 0..N`, or `where s.len <= N` for a slice")
			case b.NeverReturns:
				r.Diags.ErrorFix(fn.ClaimPos, "E303", fn.Name+" never returns, so it has no cycle bound", "drop the claim")
			case b.Cycles+thumb.IRQEntry(thumb.DefaultTiming, fn) > fn.ClaimCycles:
				got := b.Cycles + thumb.IRQEntry(thumb.DefaultTiming, fn)
				r.Diags.ErrorFix(fn.ClaimPos, "E301", fmt.Sprintf("%s takes up to %d cycles, claim is %d", fn.Name, got, fn.ClaimCycles),
					fmt.Sprintf("raise the claim to `cycles <= %d`, or remove work from the worst path", got))
			}
		}
	}
}

// CyclesLine summarizes cycle bounds for entry points (and, verbose, all).
func (r *Result) CyclesLine(all bool) string {
	if r.Bounds == nil {
		return ""
	}
	var parts []string
	show := func(fn *types.Fn) {
		b := r.Bounds[fn]
		name := fn.Name
		if fn.ISR {
			name = "isr:" + name
		}
		v := b.String()
		if fn.ISR && !b.Unbounded && !b.NeverReturns {
			v = fmt.Sprintf("%d+%d", b.Cycles, thumb.DefaultTiming.ExcEntry)
		}
		parts = append(parts, name+"="+v)
	}
	seen := map[*types.Fn]bool{}
	for _, e := range r.Program.Entries {
		show(e)
		seen[e] = true
	}
	if all {
		for _, fn := range thumb.SortedBounds(r.Bounds) {
			if !seen[fn] && !fn.Extern {
				show(fn)
			}
		}
	}
	return "CYCLES " + strings.Join(parts, " ") + " model=m33-uncalibrated"
}

// WriteNative writes prog.s, quoin_rt.s and prog.ld, then assembles and
// links them with binutils when available. It returns the files written
// and the ELF path ("" if binutils are missing).
func (r *Result) WriteNative(dir, base string, link bool) ([]string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	stem := strings.TrimSuffix(filepath.Base(base), filepath.Ext(base))
	files := []struct{ name, text string }{
		{stem + ".s", r.Asm}, {"quoin_rt.s", r.Target.Runtime}, {stem + ".ld", r.Target.Linker},
	}
	var out []string
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.text), 0o644); err != nil {
			return nil, "", err
		}
		out = append(out, p)
	}
	if !link {
		return out, "", nil
	}
	as, err1 := exec.LookPath("arm-none-eabi-as")
	ld, err2 := exec.LookPath("arm-none-eabi-ld")
	if err1 != nil || err2 != nil {
		return out, "", nil
	}
	objs := []string{filepath.Join(dir, stem+".o"), filepath.Join(dir, "quoin_rt.o")}
	for i, src := range []string{out[0], out[1]} {
		cmd := exec.Command(as, "-mcpu=cortex-m33", "-mthumb", "-o", objs[i], src)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return out, "", fmt.Errorf("assemble %s: %v\n%s", src, err, msg)
		}
	}
	elf := filepath.Join(dir, stem+".elf")
	cmd := exec.Command(ld, "-T", out[2], "-nostdlib", "--gc-sections", "-o", elf, objs[0], objs[1])
	if msg, err := cmd.CombinedOutput(); err != nil {
		return out, "", fmt.Errorf("link: %v\n%s", err, msg)
	}
	out = append(out, elf)
	if r.Target.Name == "rp2350" {
		if objcopy, err := exec.LookPath("arm-none-eabi-objcopy"); err == nil {
			bin := filepath.Join(dir, stem+".bin")
			if msg, err := exec.Command(objcopy, "-O", "binary", elf, bin).CombinedOutput(); err != nil {
				return out, elf, fmt.Errorf("objcopy: %v\n%s", err, msg)
			}
			data, err := os.ReadFile(bin)
			if err != nil {
				return out, elf, err
			}
			uf2 := filepath.Join(dir, stem+".uf2")
			if err := os.WriteFile(uf2, thumb.UF2(data, 0x20000000), 0o644); err != nil {
				return out, elf, err
			}
			out = append(out, uf2)
		}
	}
	return out, elf, nil
}

// QEMURun is the outcome of running an image on QEMU.
type QEMURun struct {
	Output   string // program output with measurement lines removed
	Exit     int
	StackHW  int64 // measured call-stack high-water mark (-1 if not reported)
	DStackHW int64
	Insns    int64 // instructions executed inside main (-1 unless counted)
	Wall     time.Duration
}

func qemuArgs(elf, log string) []string {
	args := []string{"-M", "mps2-an505", "-cpu", "cortex-m33", "-nographic", "-monitor", "none",
		"-semihosting-config", "enable=on,target=native", "-no-reboot", "-kernel", elf}
	if log != "" {
		args = append(args, "-accel", "tcg,one-insn-per-tb=on", "-d", "exec,nochain", "-D", log)
	}
	return args
}

func runQEMU(args []string, timeout time.Duration) (string, int, time.Duration, error) {
	q, err := exec.LookPath("qemu-system-arm")
	if err != nil {
		return "", -1, 0, fmt.Errorf("qemu-system-arm not installed")
	}
	cmd := exec.Command(q, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "", -1, 0, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(timeout):
		cmd.Process.Kill()
		<-done
		return stdout.String(), -1, time.Since(start), fmt.Errorf("qemu timed out after %s", timeout)
	}
	return stdout.String(), cmd.ProcessState.ExitCode(), time.Since(start), nil
}

// RunQEMU runs an m33-qemu ELF. With countInsns it single-steps QEMU and
// counts instructions from main's entry to its return.
func RunQEMU(elf string, countInsns bool, timeout time.Duration) (*QEMURun, error) {
	var logFile string
	if countInsns {
		f, err := os.CreateTemp("", "quoin-qemu-*.log")
		if err != nil {
			return nil, err
		}
		f.Close()
		logFile = f.Name()
		defer os.Remove(logFile)
	}
	out, code, wall, err := runQEMU(qemuArgs(elf, logFile), timeout)
	if err != nil {
		return nil, err
	}
	run := &QEMURun{Exit: code, StackHW: -1, DStackHW: -1, Insns: -1, Wall: wall}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "STACKHW "):
			run.StackHW, _ = strconv.ParseInt(strings.TrimPrefix(l, "STACKHW "), 10, 64)
		case strings.HasPrefix(l, "DSTACKHW "):
			run.DStackHW, _ = strconv.ParseInt(strings.TrimPrefix(l, "DSTACKHW "), 10, 64)
		default:
			lines = append(lines, l)
		}
	}
	run.Output = strings.Join(lines, "\n")
	if countInsns {
		pcs, err := tracePCs(logFile)
		if err != nil {
			return run, err
		}
		mainAddr, err1 := symbol(elf, "main")
		after, err2 := symbol(elf, "ql_after_main")
		if err1 != nil || err2 != nil {
			return run, fmt.Errorf("main or ql_after_main missing")
		}
		run.Insns = between(pcs, mainAddr, func(uint64) uint64 { return after })
	}
	return run, nil
}

// CountFn runs an m33-qemu ELF single-stepped and returns the instructions
// executed by the first complete call of fn, from entry to return.
func CountFn(elf, fn string, timeout time.Duration) (int64, error) {
	f, err := os.CreateTemp("", "quoin-qemu-*.log")
	if err != nil {
		return -1, err
	}
	f.Close()
	defer os.Remove(f.Name())
	if _, _, _, err := runQEMU(qemuArgs(elf, f.Name()), timeout); err != nil {
		return -1, err
	}
	addr, err := symbol(elf, fn)
	if err != nil {
		return -1, err
	}
	pcs, err := tracePCs(f.Name())
	if err != nil {
		return -1, err
	}
	// The return address follows the 4-byte BL executed just before entry.
	n := between(pcs, addr, func(prev uint64) uint64 { return prev + 4 })
	if n < 0 {
		return -1, fmt.Errorf("no complete call of %s in the trace", fn)
	}
	return n, nil
}

// between counts trace steps from the first visit of entry until the PC
// reaches the return address computed from the PC before entry.
func between(pcs []uint64, entry uint64, ret func(prev uint64) uint64) int64 {
	for i, pc := range pcs {
		if pc != entry || i == 0 {
			continue
		}
		r := ret(pcs[i-1])
		for j := i + 1; j < len(pcs); j++ {
			if pcs[j] == r {
				return int64(j - i)
			}
		}
		return -1
	}
	return -1
}

func symbol(elf, name string) (uint64, error) {
	nm, err := exec.LookPath("arm-none-eabi-nm")
	if err != nil {
		return 0, err
	}
	out, err := exec.Command(nm, elf).Output()
	if err != nil {
		return 0, err
	}
	var clone uint64
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 3 && f[2] == name {
			v, _ := strconv.ParseUint(f[0], 16, 64)
			return v &^ 1, nil
		}
		// GCC's IPA passes clone functions as name.isra.0, name.constprop.0.
		if len(f) == 3 && clone == 0 && strings.HasPrefix(f[2], name+".") {
			clone, _ = strconv.ParseUint(f[0], 16, 64)
		}
	}
	if clone != 0 {
		return clone &^ 1, nil
	}
	return 0, fmt.Errorf("symbol %s not found", name)
}

// tracePCs extracts executed PCs from a QEMU `-d exec` log, whose lines
// look like "Trace 0: 0x7f.. [00000000/10000080/00000000/ff200000] main".
func tracePCs(log string) ([]uint64, error) {
	data, err := os.ReadFile(log)
	if err != nil {
		return nil, err
	}
	var pcs []uint64
	for _, l := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(l, "Trace ") {
			continue
		}
		k := strings.Index(l, "[")
		e := strings.Index(l, "]")
		if k < 0 || e < k {
			continue
		}
		fields := strings.Split(l[k+1:e], "/")
		if len(fields) < 2 {
			continue
		}
		pc, err := strconv.ParseUint(fields[1], 16, 64)
		if err == nil {
			pcs = append(pcs, pc)
		}
	}
	return pcs, nil
}

// FnSizes returns the .text size in bytes of each function symbol in elf.
func FnSizes(elf string) (map[string]int64, error) {
	nm, err := exec.LookPath("arm-none-eabi-nm")
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(nm, "-S", elf).Output()
	if err != nil {
		return nil, err
	}
	m := map[string]int64{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 4 && (f[2] == "t" || f[2] == "T") {
			n, _ := strconv.ParseInt(f[1], 16, 64)
			m[f[3]] = n
		}
	}
	return m, nil
}
