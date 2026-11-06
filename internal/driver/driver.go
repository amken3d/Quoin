// Package driver runs the Quoin pipeline: parse, check, analyze, emit.
package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/cgen"
	"github.com/amken3d/quoin/internal/check"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/parser"
	"github.com/amken3d/quoin/internal/stack"
	"github.com/amken3d/quoin/internal/thumb"
	"github.com/amken3d/quoin/internal/types"
)

// Source is one input file.
type Source struct {
	Name string
	Text []byte
}

// Options configure a compile.
type Options struct {
	Budget   int64
	SU       map[string]int64
	ExcFrame int64  // bytes pushed per interrupt; 0 means 32 (no FPU context)
	Target   string // "c" (default), "rp2350" or "m33-qemu"
	Lines    bool
	Emit     bool // generate C
}

// Result is everything a compile produced.
type Result struct {
	Diags   diag.List
	Program *check.Program
	Report  *stack.Report
	C       string
	LD      string
	// Native targets.
	Asm    string
	Target *thumb.Target
	Bounds map[*types.Fn]thumb.Bound
}

// Compile runs the pipeline over all sources as one program.
func Compile(srcs []Source, opt Options) *Result {
	r := &Result{}
	var files []*ast.File
	for _, s := range srcs {
		files = append(files, parser.Parse(s.Name, s.Text, &r.Diags))
	}
	if r.Diags.HasErrors() {
		// Type errors after a syntax error are mostly cascades; report the
		// syntax errors alone so the next edit targets the real cause.
		r.Diags.Sort()
		return r
	}
	r.Program = check.Check(files, &r.Diags)
	if tgt, ok := thumb.Targets[opt.Target]; ok && !r.Diags.HasErrors() {
		r.native(tgt, opt)
		r.Diags.Sort()
		return r
	}
	if opt.Target != "" && opt.Target != "c" && thumb.Targets[opt.Target] == nil {
		r.Diags.ErrorFix(diag.Pos{Line: 1, Col: 1}, "E208", "unknown target "+opt.Target, "use c, rp2350 or m33-qemu")
		return r
	}
	if !r.Diags.HasErrors() {
		r.Report = stack.Analyze(r.Program, stack.Options{Budget: opt.Budget, SU: opt.SU, ExcFrame: opt.ExcFrame}, &r.Diags)
		if r.Report.Valid {
			r.checkClaims(false)
		}
	}
	r.Diags.Sort()
	if opt.Emit && !r.Diags.HasErrors() && r.Report != nil && r.Report.Valid {
		name := "program"
		if len(srcs) > 0 {
			name = filepath.Base(srcs[0].Name)
		}
		r.C = cgen.Generate(r.Program, cgen.Options{
			Source:    name,
			Lines:     opt.Lines,
			DataStack: r.Report.DataTotal,
			CallStack: r.Report.CallTotal,
			StackSrc:  r.Report.Source,
		})
		r.LD = cgen.LinkerFragment(r.Report.CallTotal, r.Report.DataTotal)
	}
	return r
}

// Load reads source files from disk.
func Load(paths []string) ([]Source, error) {
	var srcs []Source
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, Source{Name: p, Text: b})
	}
	return srcs, nil
}

// CheckWarnings renders one W101 line per runtime check, for --checks.
func (r *Result) CheckWarnings() string {
	if r.Program == nil {
		return ""
	}
	var b strings.Builder
	for _, fn := range r.Program.Fns {
		for _, c := range fn.Checks {
			d := diag.Diag{Code: "W101", Kind: diag.KindOf("W101"), Pos: c.Pos, Msg: c.What + " in " + fn.Name}
			b.WriteString(d.Format())
			b.WriteString("\n")
		}
	}
	return b.String()
}

// WriteOutputs writes the C file, runtime header and linker fragment.
func (r *Result) WriteOutputs(dir, base string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(filepath.Base(base), filepath.Ext(base))
	files := map[string]string{
		stem + ".c":      r.C,
		"quoin_rt.h":     cgen.RuntimeHeader,
		"quoin_stack.ld": r.LD,
	}
	var out []string
	for _, n := range []string{stem + ".c", "quoin_rt.h", "quoin_stack.ld"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(files[n]), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", p, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// EntryNames lists the program's entry points, for messages.
func EntryNames(p *check.Program) []string {
	var names []string
	for _, e := range p.Entries {
		names = append(names, e.Name)
	}
	return names
}
