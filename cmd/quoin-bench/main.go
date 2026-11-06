// Command quoin-bench measures the figures of merit that can be measured
// without hardware or model calls, over testdata/kernels:
//
//   - verification: proof coverage without annotations, where clauses per
//     100 lines, checker time (p99 over repeated runs; zero timeouts);
//   - bound tightness on QEMU: proven stack / measured stack and static
//     instruction bound / executed instructions (cycle tightness needs
//     DWT_CYCCNT on an RP2350 and is reported as pending);
//   - resources: flash (.text) and RAM (data + bss + proven stack), and
//     executed instructions and code size of each kernel against the same
//     kernel through the C backend and GCC -O2 on the same QEMU core;
//   - toolchain: whole-program compile time and install size.
//
// usage: quoin-bench [-n runs] [-json out.json] [dir]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/amken3d/quoin/internal/driver"
)

type kernel struct {
	Name        string  `json:"name"`
	File        string  `json:"file"`
	Lines       int     `json:"lines"`
	Where       int     `json:"where_clauses"`
	Sites       int     `json:"check_sites"`
	Checks      int     `json:"runtime_checks"`
	Coverage    float64 `json:"proof_coverage"`
	CheckP99us  float64 `json:"checker_p99_us"`
	CompileMSus float64 `json:"compile_median_us"`
	StackBound  int64   `json:"stack_bound"`
	StackMeas   int64   `json:"stack_measured"`
	InsnBound   int64   `json:"insn_bound"`
	InsnMeas    int64   `json:"insn_measured"`
	CycleBound  int64   `json:"cycle_bound_model"`
	TextBytes   int64   `json:"image_text_bytes"`
	RAMBytes    int64   `json:"ram_bytes"`
	FnBytes     int64   `json:"kernel_bytes"`
	GCCInsns    int64   `json:"gcc_o2_insns"`
	GCCFnBytes  int64   `json:"gcc_o2_kernel_bytes"`
	Correct     bool    `json:"output_correct"`
	Err         string  `json:"error,omitempty"`
}

var kernelRe = regexp.MustCompile(`(?m)^// kernel: (\w+)`)

func main() {
	runs := flag.Int("n", 30, "repetitions for timing")
	jsonOut := flag.String("json", "", "write results as JSON to this file")
	flag.Parse()
	dir := "testdata/kernels"
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.qn"))
	sort.Strings(files)
	var ks []kernel
	for _, f := range files {
		ks = append(ks, measure(f, *runs))
	}
	report(ks)
	if *jsonOut != "" {
		data, _ := json.MarshalIndent(map[string]any{"date": time.Now().UTC().Format(time.RFC3339), "env": env(), "kernels": ks}, "", "  ")
		os.WriteFile(*jsonOut, data, 0o644)
	}
}

func measure(f string, runs int) kernel {
	src, _ := os.ReadFile(f)
	k := kernel{File: filepath.Base(f), StackMeas: -1, InsnMeas: -1, GCCInsns: -1}
	if m := kernelRe.FindSubmatch(src); m != nil {
		k.Name = string(m[1])
	}
	for _, l := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "//") {
			k.Lines++
		}
	}
	k.Where = strings.Count(string(src), " where ")
	srcs := []driver.Source{{Name: k.File, Text: src}}

	// Checker time: check + stack analysis, no emission.
	var checkT []time.Duration
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		driver.Compile(srcs, driver.Options{})
		checkT = append(checkT, time.Since(t0))
	}
	k.CheckP99us = pct(checkT, 0.99)
	var compT []time.Duration
	var r *driver.Result
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		r = driver.Compile(srcs, driver.Options{Target: "m33-qemu"})
		compT = append(compT, time.Since(t0))
	}
	k.CompileMSus = pct(compT, 0.5)
	if r.Diags.HasErrors() {
		k.Err = strings.TrimSpace(r.Diags.Text())
		return k
	}
	for _, fn := range r.Program.Fns {
		k.Sites += fn.Sites
		k.Checks += len(fn.Checks)
	}
	if k.Sites > 0 {
		k.Coverage = 1 - float64(k.Checks)/float64(k.Sites)
	} else {
		k.Coverage = 1
	}
	k.StackBound = r.Report.Entries[0].Call
	for fn, b := range r.Bounds {
		if fn.Name == k.Name {
			k.InsnBound, k.CycleBound = b.Insns, b.Cycles
		}
	}
	tmp, _ := os.MkdirTemp("", "quoin-bench-")
	defer os.RemoveAll(tmp)
	_, elf, err := r.WriteNative(tmp, f, true)
	if err != nil || elf == "" {
		k.Err = fmt.Sprint("link: ", err)
		return k
	}
	run, err := driver.RunQEMU(elf, false, time.Minute)
	if err != nil {
		k.Err = err.Error()
		return k
	}
	k.Correct = strings.TrimSpace(run.Output) == want(string(src))
	k.StackMeas = run.StackHW
	k.InsnMeas, _ = driver.CountFn(elf, "qn_"+k.Name, time.Minute)
	if sz, err := driver.FnSizes(elf); err == nil {
		k.FnBytes = sz["qn_"+k.Name]
	}
	k.TextBytes, k.RAMBytes = sizes(elf)
	k.RAMBytes += k.StackBound + r.Report.DataTotal
	k.GCCInsns, k.GCCFnBytes = gcc(f, src, tmp, k.Name)
	return k
}

// gcc builds the same kernel through the C backend with arm-none-eabi-gcc
// -O2 for Cortex-M33, links it with the QEMU runtime and counts the
// kernel's executed instructions. Quoin functions are marked noinline so the
// kernel stays a separate function.
func gcc(f string, src []byte, tmp, name string) (int64, int64) {
	cc, err := exec.LookPath("arm-none-eabi-gcc")
	if err != nil {
		return -1, -1
	}
	r := driver.Compile([]driver.Source{{Name: filepath.Base(f), Text: src}}, driver.Options{Emit: true, Lines: false})
	if r.C == "" {
		return -1, -1
	}
	d := filepath.Join(tmp, "c")
	if _, err := r.WriteOutputs(d, f); err != nil {
		return -1, -1
	}
	q := driver.Compile([]driver.Source{{Name: filepath.Base(f), Text: src}}, driver.Options{Target: "m33-qemu"})
	files, _, err := q.WriteNative(filepath.Join(tmp, "rt"), f, false)
	if err != nil {
		return -1, -1
	}
	stem := strings.TrimSuffix(filepath.Base(f), ".qn")
	stub := filepath.Join(d, "dstack.s")
	os.WriteFile(stub, []byte(cStub), 0o644)
	elf := filepath.Join(d, stem+".elf")
	args := []string{"-std=c99", "-O2", "-mcpu=cortex-m33", "-mthumb", "-nostdlib", "-nostartfiles", "-ffreestanding",
		"-DQL_FN=static __attribute__((noinline))", "-T", files[2], "-o", elf,
		filepath.Join(d, stem+".c"), files[1], stub, "-Wl,--gc-sections"}
	if msg, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "gcc %s: %v\n%s", stem, err, msg)
		return -1, -1
	}
	n, err := driver.CountFn(elf, "qn_"+name, time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gcc %s: %v\n", stem, err)
		return -1, -1
	}
	sz, _ := driver.FnSizes(elf)
	for sym, b := range sz {
		if sym == "qn_"+name || strings.HasPrefix(sym, "qn_"+name+".") {
			return n, b
		}
	}
	return n, -1
}

// cStub supplies what freestanding GCC output may call (memset/memcpy for
// aggregate initialization and copies) and the data-stack symbols the QEMU
// runtime reports on.
const cStub = `	.syntax unified
	.thumb
	.section .vectors, "a"
	.align 7
	.word _stack_top
	.word _start + 1
	.rept 14
	.word ql_unhandled + 1
	.endr
	.global ql_dstack, ql_dstack_end, memset, memcpy
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
memcpy:
	mov r3, r0
3:	cbz r2, 4f
	ldrb r12, [r1], #1
	strb r12, [r3], #1
	subs r2, #1
	b 3b
4:	bx lr
`

func sizes(elf string) (text, ram int64) {
	out, err := exec.Command("arm-none-eabi-size", "-A", elf).Output()
	if err != nil {
		return 0, 0
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		var n int64
		fmt.Sscan(f[1], &n)
		switch f[0] {
		case ".text":
			text = n
		case ".data", ".bss":
			ram += n
		}
	}
	return
}

func want(src string) string {
	i := strings.Index(src, "// want:")
	if i < 0 {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(src[i:], "\n")[1:] {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "//") {
			break
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(l, "//")))
	}
	return strings.Join(lines, "\n")
}

func pct(ds []time.Duration, p float64) float64 {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	i := int(p * float64(len(ds)-1))
	return float64(ds[i].Microseconds())
}

func ratio(a, b int64) string {
	if a <= 0 || b <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", float64(a)/float64(b))
}

func report(ks []kernel) {
	fmt.Println("## Verification")
	fmt.Println()
	fmt.Println("| kernel | lines | where/100 lines | check sites | runtime checks | proof coverage | checker p99 | output |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |")
	var sites, checks, lines, where int
	for _, k := range ks {
		ok := "correct"
		if !k.Correct {
			ok = "WRONG " + k.Err
		}
		fmt.Printf("| %s | %d | %.1f | %d | %d | %.0f%% | %.0f µs | %s |\n", k.Name, k.Lines, 100*float64(k.Where)/float64(max(k.Lines, 1)),
			k.Sites, k.Checks, 100*k.Coverage, k.CheckP99us, ok)
		sites += k.Sites
		checks += k.Checks
		lines += k.Lines
		where += k.Where
	}
	fmt.Printf("| **all** | %d | %.1f | %d | %d | %.0f%% | | |\n", lines, 100*float64(where)/float64(max(lines, 1)), sites, checks,
		100*(1-float64(checks)/float64(max(sites, 1))))
	fmt.Println()
	fmt.Println("## Bound tightness (QEMU mps2-an505, Cortex-M33)")
	fmt.Println()
	fmt.Println("| kernel | stack proven / measured | ratio | insns static / executed | ratio | cycle bound (model) | cycle ratio |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | --- |")
	viol := 0
	for _, k := range ks {
		if k.StackMeas > k.StackBound || k.InsnMeas > k.InsnBound {
			viol++
		}
		fmt.Printf("| %s | %d / %d | %s | %d / %d | %s | %d | needs RP2350 DWT |\n", k.Name, k.StackBound, k.StackMeas, ratio(k.StackBound, k.StackMeas),
			k.InsnBound, k.InsnMeas, ratio(k.InsnBound, k.InsnMeas), k.CycleBound)
	}
	fmt.Printf("\nBound violations: %d\n\n", viol)
	fmt.Println("## Resources and code quality vs GCC -O2 (same kernel, C backend; executed instructions on QEMU)")
	fmt.Println()
	fmt.Println("| kernel | image .text | RAM (data+bss+stack) | kernel bytes | GCC -O2 bytes | size ratio | kernel insns | GCC -O2 insns | insn ratio | compile (median) |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, k := range ks {
		fmt.Printf("| %s | %d | %d | %d | %d | %s | %d | %d | %s | %.0f µs |\n", k.Name, k.TextBytes, k.RAMBytes, k.FnBytes, k.GCCFnBytes,
			ratio(k.FnBytes, k.GCCFnBytes), k.InsnMeas, k.GCCInsns, ratio(k.InsnMeas, k.GCCInsns), k.CompileMSus)
	}
	fmt.Println()
	e := env()
	fmt.Println("## Toolchain")
	fmt.Println()
	for _, k := range []string{"quoinc_bytes", "binutils_bytes", "gcc_toolchain_bytes", "qemu", "gcc"} {
		fmt.Printf("- %s: %v\n", k, e[k])
	}
}

func env() map[string]any {
	m := map[string]any{}
	if exe, err := os.Executable(); err == nil {
		// The benchmark links the compiler into itself; build quoinc to size it.
		_ = exe
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(os.TempDir(), "quoinc-size"), "./cmd/quoinc").CombinedOutput(); err == nil {
		if st, err := os.Stat(filepath.Join(os.TempDir(), "quoinc-size")); err == nil {
			m["quoinc_bytes"] = st.Size()
		}
	} else {
		m["quoinc_bytes"] = string(out)
	}
	var bin int64
	for _, t := range []string{"arm-none-eabi-as", "arm-none-eabi-ld"} {
		if p, err := exec.LookPath(t); err == nil {
			if st, err := os.Stat(p); err == nil {
				bin += st.Size()
			}
		}
	}
	m["binutils_bytes"] = bin
	if out, err := exec.Command("du", "-sb", "/usr/lib/gcc/arm-none-eabi").Output(); err == nil {
		var n int64
		fmt.Sscan(string(out), &n)
		m["gcc_toolchain_bytes"] = n
	}
	if out, err := exec.Command("qemu-system-arm", "--version").Output(); err == nil {
		m["qemu"] = strings.SplitN(string(out), "\n", 2)[0]
	}
	if out, err := exec.Command("arm-none-eabi-gcc", "--version").Output(); err == nil {
		m["gcc"] = strings.SplitN(string(out), "\n", 2)[0]
	}
	return m
}
