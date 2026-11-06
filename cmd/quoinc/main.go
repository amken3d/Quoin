// Command quoinc is the Quoin compiler.
//
//	quoinc check [flags] file.qn...        diagnostics + worst-case stack line
//	quoinc build [flags] file.qn... [-o d] also write C99, quoin_rt.h, quoin_stack.ld
//	quoinc stack --su DIR file.qn...       exact bounds from gcc -fstack-usage
//	quoinc run --target m33-qemu file.qn   build natively and run under QEMU
//	quoinc explain E107                    describe a diagnostic code
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/driver"
	"github.com/amken3d/quoin/internal/stack"
)

const usage = `quoinc - the Quoin compiler

usage:
  quoinc check   [flags] file.qn...   check, print diagnostics and STACK/CHECKS lines
  quoinc build   [flags] file.qn...   also emit C99 to -o (default ./build)
  quoinc run     [flags] file.qn...   build for m33-qemu and run under QEMU (measures stack)
  quoinc stack   --su PATH [flags] file.qn...
                                      worst-case stack from gcc -fstack-usage files
  quoinc explain CODE                 describe a diagnostic code (E107, W101, ...)

flags:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		newFlags(stderr).PrintDefaults()
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "explain":
		return explain(rest, stdout, stderr)
	case "check", "build", "stack", "run":
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		newFlags(stdout).PrintDefaults()
		return 0
	default:
		fmt.Fprintf(stderr, "quoinc: unknown command %q\n", cmd)
		return 2
	}
	fs := newFlags(stderr)
	budget := fs.Int64("budget", 0, "fail with E202 if worst-case call+data stack exceeds this many bytes")
	asJSON := fs.Bool("json", false, "print one JSON object instead of text")
	verbose := fs.Bool("v", false, "print worst-case paths per entry point")
	checks := fs.Bool("checks", false, "print a W101 line for every runtime check inserted")
	noLine := fs.Bool("no-line", false, "omit #line directives from generated C")
	outDir := fs.String("o", "build", "output directory for build")
	su := fs.String("su", "", "comma-separated .su files or directories (gcc -fstack-usage)")
	excFrame := fs.Int64("exc-frame", 32, "bytes the core pushes per interrupt (32 on Cortex-M; 104 with an FPU context)")
	target := fs.String("target", "c", "c (C99 for any toolchain), rp2350 (native Thumb-2, full claims), m33-qemu (native, for QEMU)")
	noLink := fs.Bool("no-link", false, "native targets: write assembly only, do not assemble or link")
	insns := fs.Bool("insns", false, "run: count instructions executed in main (slow)")
	if err := fs.Parse(interleave(rest)); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(stderr, "quoinc: no input files")
		return 2
	}
	srcs, err := driver.Load(files)
	if err != nil {
		fmt.Fprintln(stderr, "quoinc:", err)
		return 2
	}
	if cmd == "run" {
		*target = "m33-qemu"
	}
	opt := driver.Options{Budget: *budget, Lines: !*noLine, Emit: cmd == "build", ExcFrame: *excFrame, Target: *target}
	if cmd == "stack" {
		if *su == "" {
			fmt.Fprintln(stderr, "quoinc: stack needs --su PATH (compile the C with -fstack-usage)")
			return 2
		}
		m, err := stack.ReadSU(strings.Split(*su, ","))
		if err != nil {
			fmt.Fprintln(stderr, "quoinc:", err)
			return 2
		}
		opt.SU = m
	} else if *su != "" {
		m, err := stack.ReadSU(strings.Split(*su, ","))
		if err != nil {
			fmt.Fprintln(stderr, "quoinc:", err)
			return 2
		}
		opt.SU = m
	}
	r := driver.Compile(srcs, opt)
	var written []string
	var elf string
	if cmd == "build" && r.C != "" {
		written, err = r.WriteOutputs(*outDir, files[0])
		if err != nil {
			fmt.Fprintln(stderr, "quoinc:", err)
			return 2
		}
	}
	if (cmd == "build" || cmd == "run") && r.Asm != "" {
		dir := *outDir
		if cmd == "run" {
			dir, _ = os.MkdirTemp("", "quoinc-run-")
			defer os.RemoveAll(dir)
		}
		written, elf, err = r.WriteNative(dir, files[0], !*noLink || cmd == "run")
		if err != nil {
			fmt.Fprintln(stderr, "quoinc:", err)
			return 2
		}
		if cmd == "run" {
			written = nil
		}
	}
	if *asJSON {
		printJSON(stdout, r, written)
	} else {
		fmt.Fprint(stdout, r.Diags.Text())
		if *checks {
			fmt.Fprint(stdout, r.CheckWarnings())
		}
		if r.Report != nil {
			fmt.Fprintln(stdout, r.Report.Line())
			if l := r.CyclesLine(*verbose); l != "" {
				fmt.Fprintln(stdout, l)
			}
			fmt.Fprintln(stdout, r.Report.ChecksLine())
			if *verbose || cmd == "stack" {
				fmt.Fprint(stdout, r.Report.Detail())
			}
		}
		for _, w := range written {
			fmt.Fprintln(stdout, "WROTE", w)
		}
	}
	if r.Diags.HasErrors() {
		return 1
	}
	if cmd == "run" {
		if elf == "" {
			fmt.Fprintln(stderr, "quoinc: run needs arm-none-eabi-as/ld")
			return 2
		}
		res, err := driver.RunQEMU(elf, *insns, 60*time.Second)
		if err != nil {
			fmt.Fprintln(stderr, "quoinc:", err)
			return 2
		}
		fmt.Fprintln(stdout, res.Output)
		if res.StackHW >= 0 && r.Report != nil && len(r.Report.Entries) > 0 {
			bound := r.Report.Entries[0].Call
			fmt.Fprintf(stdout, "MEASURED stack=%dB (bound %dB) data=%dB (bound %dB)", res.StackHW, bound, res.DStackHW, r.Report.DataTotal)
			if res.Insns >= 0 {
				fmt.Fprintf(stdout, " insns=%d", res.Insns)
				if b := r.Bounds[r.Program.Entries[0]]; !b.Unbounded && !b.NeverReturns {
					fmt.Fprintf(stdout, " (bound %d)", b.Insns)
				}
			}
			fmt.Fprintln(stdout)
		}
		return res.Exit
	}
	return 0
}

func newFlags(w io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("quoinc", flag.ContinueOnError)
	fs.SetOutput(w)
	return fs
}

// interleave lets flags follow file names (Go's flag package stops at the
// first positional argument).
func interleave(args []string) []string {
	var flags, pos []string
	takesValue := map[string]bool{"-budget": true, "--budget": true, "-o": true, "--o": true, "-su": true, "--su": true,
		"-exc-frame": true, "--exc-frame": true, "-target": true, "--target": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if takesValue[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func printJSON(w io.Writer, r *driver.Result, written []string) {
	type entry struct {
		Name     string   `json:"name"`
		ISR      bool     `json:"isr"`
		Call     int64    `json:"call_bytes"`
		Data     int64    `json:"data_bytes"`
		CallPath []string `json:"call_path"`
	}
	out := struct {
		OK      bool        `json:"ok"`
		Diags   []diag.Diag `json:"diagnostics"`
		Stack   any         `json:"stack,omitempty"`
		Checks  any         `json:"runtime_checks,omitempty"`
		Written []string    `json:"written,omitempty"`
	}{OK: !r.Diags.HasErrors(), Diags: r.Diags.Items, Written: written}
	if out.Diags == nil {
		out.Diags = []diag.Diag{}
	}
	if rep := r.Report; rep != nil && rep.Valid {
		var es []entry
		for _, e := range rep.Entries {
			var path []string
			for _, f := range e.CallPath {
				path = append(path, f.Fn.Name)
			}
			es = append(es, entry{e.Fn.Name, e.Fn.ISR, e.Call, e.Data, path})
		}
		out.Stack = map[string]any{
			"entries": es, "call_total": rep.CallTotal, "data_total": rep.DataTotal,
			"total": rep.Total, "budget": rep.Budget, "source": rep.Source,
		}
		out.Checks = map[string]any{"total": rep.NChecks, "by_kind": rep.Checks}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

func explain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		for _, c := range diag.Codes {
			fmt.Fprintf(stdout, "%s %s\n", c.Code, c.Kind)
		}
		return 0
	}
	code := strings.ToUpper(args[0])
	c, ok := diag.Lookup(code)
	if !ok {
		fmt.Fprintf(stderr, "quoinc: unknown code %s\n", code)
		return 2
	}
	fmt.Fprintf(stdout, "%s %s\n  %s\n", c.Code, c.Kind, c.Explain)
	return 0
}
