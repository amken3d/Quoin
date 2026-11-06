// Package stack computes worst-case call-stack and data-stack bytes per
// entry point, and enforces the cross-entry rules (no shared globals,
// single-producer single-consumer queues).
//
// Quoin forbids recursion and function values, so the call graph is a DAG
// rooted at main and each isr, and the worst case is a longest-path walk.
// Data-stack frames are exact: the checker lays them out. Call-stack frames
// are estimates until gcc's -fstack-usage output is supplied, at which point
// they are the compiler's own numbers (the GNATstack approach).
package stack

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/amken3d/quoin/internal/check"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

// ExceptionFrame is what a Cortex-M core pushes on interrupt entry without
// an active FPU context (r0-r3, r12, lr, pc, xpsr).
const ExceptionFrame = 32

// Options control the analysis.
type Options struct {
	Budget   int64            // 0: no budget
	SU       map[string]int64 // C function -> frame bytes, from gcc -fstack-usage
	ExcFrame int64            // bytes the core pushes per interrupt; default ExceptionFrame
	Source   string           // label for the call-frame source; default "est" or "su"
}

// Frame is one function on a worst-case path.
type Frame struct {
	Fn    *types.Fn
	Bytes int64
}

// Entry is the result for one entry point.
type Entry struct {
	Fn       *types.Fn
	Call     int64
	Data     int64
	CallPath []Frame
	DataPath []Frame
}

// Report is the whole-program result.
type Report struct {
	Entries   []Entry
	CallTotal int64
	DataTotal int64
	Total     int64
	Budget    int64
	Source    string // "est" or "su"
	Checks    map[string]int
	NChecks   int
	Valid     bool // false when recursion or unknown extern stacks prevent a bound
	// FnCall is the worst-case call-stack bytes of every function reached
	// from an entry point (its frame plus its deepest callee chain).
	FnCall map[*types.Fn]int64
}

type result struct {
	call, data int64
	callPath   []Frame
	dataPath   []Frame
}

type analyzer struct {
	p            *check.Program
	opt          Options
	d            *diag.List
	memo         map[*types.Fn]*result
	valid        bool
	warnedExtern map[*types.Fn]bool
}

// Analyze computes the report. Errors (recursion, unknown extern stack,
// shared state, queue roles, budget) go to d.
func Analyze(p *check.Program, opt Options, d *diag.List) *Report {
	if opt.ExcFrame == 0 {
		opt.ExcFrame = ExceptionFrame
	}
	a := &analyzer{p: p, opt: opt, d: d, memo: map[*types.Fn]*result{}, valid: true, warnedExtern: map[*types.Fn]bool{}}
	r := &Report{Budget: opt.Budget, Source: "est", Checks: map[string]int{}}
	if opt.SU != nil {
		r.Source = "su"
	}
	if opt.Source != "" {
		r.Source = opt.Source
	}
	if !a.acyclic() {
		r.Valid = false
		return r
	}
	for _, e := range p.Entries {
		res := a.walk(e)
		ent := Entry{Fn: e, Call: res.call, Data: res.data, CallPath: res.callPath, DataPath: res.dataPath}
		if e.ISR {
			ent.Call += opt.ExcFrame
		}
		r.Entries = append(r.Entries, ent)
		r.CallTotal += ent.Call
		r.DataTotal += ent.Data
	}
	r.Total = r.CallTotal + r.DataTotal
	r.Valid = a.valid
	r.FnCall = map[*types.Fn]int64{}
	for fn, res := range a.memo {
		r.FnCall[fn] = res.call
	}
	seen := map[*types.Fn]bool{}
	for _, e := range p.Entries {
		for _, fn := range reach(e) {
			if !seen[fn] {
				seen[fn] = true
				for _, cs := range fn.Checks {
					kind := cs.What
					if i := strings.IndexByte(kind, ' '); i > 0 {
						kind = kind[:i]
					}
					r.Checks[kind]++
					r.NChecks++
				}
			}
		}
	}
	a.shared()
	if r.Valid && opt.Budget > 0 && r.Total > opt.Budget && len(r.Entries) > 0 {
		main := r.Entries[0]
		fix := "worst call path " + pathString(main.CallPath)
		if r.DataTotal > 0 {
			big := biggest(r)
			if big.Fn != nil {
				fix = fmt.Sprintf("largest data frame: %s %dB; worst call path %s", big.Fn.Name, big.Bytes, pathString(main.CallPath))
			}
		}
		d.ErrorFix(main.Fn.Pos, "E202", fmt.Sprintf("worst case %dB exceeds budget %dB (call %dB + data %dB)", r.Total, opt.Budget, r.CallTotal, r.DataTotal), fix)
	}
	return r
}

func biggest(r *Report) Frame {
	var best Frame
	for _, e := range r.Entries {
		for _, f := range e.DataPath {
			if f.Bytes > best.Bytes {
				best = f
			}
		}
	}
	return best
}

// acyclic reports recursion as E201 and returns false if any cycle exists.
func (a *analyzer) acyclic() bool {
	const (
		white = iota
		grey
		black
	)
	color := map[*types.Fn]int{}
	var stack []*types.Fn
	ok := true
	var visit func(fn *types.Fn)
	visit = func(fn *types.Fn) {
		color[fn] = grey
		stack = append(stack, fn)
		for _, c := range fn.Calls {
			switch color[c.Callee] {
			case white:
				visit(c.Callee)
			case grey:
				ok = false
				var names []string
				start := 0
				for i, f := range stack {
					if f == c.Callee {
						start = i
					}
				}
				for _, f := range stack[start:] {
					names = append(names, f.Name)
				}
				names = append(names, c.Callee.Name)
				a.d.ErrorFix(c.Pos, "E201", "recursion "+strings.Join(names, " > "), "rewrite as a loop; recursion has no static stack bound")
			}
		}
		stack = stack[:len(stack)-1]
		color[fn] = black
	}
	for _, e := range a.p.Entries {
		if color[e] == white {
			visit(e)
		}
	}
	return ok
}

func (a *analyzer) frame(fn *types.Fn) int64 {
	if a.opt.SU != nil && !fn.Extern {
		if b, ok := a.opt.SU[fn.CName]; ok {
			return b
		}
		if fn.Main || fn.ISR {
			a.valid = false
			if !a.warnedExtern[fn] {
				a.warnedExtern[fn] = true
				a.d.ErrorFix(fn.Pos, "E207", fmt.Sprintf("no -fstack-usage entry for %s", fn.CName),
					"compile every generated .c file with -fstack-usage and pass all the .su files")
			}
			return 0
		}
		// A static function gcc did not emit was inlined into its callers,
		// whose frames already include it.
		return 0
	}
	if a.opt.SU != nil {
		if b, ok := a.opt.SU[fn.CName]; ok {
			return b
		}
	}
	if fn.Extern {
		if fn.StackDecl < 0 {
			a.valid = false
			if !a.warnedExtern[fn] {
				a.warnedExtern[fn] = true
				a.d.ErrorFix(fn.Pos, "E203", fmt.Sprintf("extern fn %s has no stack bound", fn.Name),
					"add `stack <bytes>` to the declaration (measure with gcc -fstack-usage)")
			}
			return 0
		}
		return fn.StackDecl
	}
	return fn.FrameEst
}

func (a *analyzer) walk(fn *types.Fn) *result {
	if r, ok := a.memo[fn]; ok {
		return r
	}
	own := a.frame(fn)
	best := &result{}
	if len(fn.Checks) > 0 {
		trap := int64(8)
		if a.opt.SU != nil {
			if b, ok := a.opt.SU["ql_trap"]; ok {
				trap = b
			}
		}
		best.call = trap
	}
	for _, c := range fn.Calls {
		r := a.walk(c.Callee)
		if r.call > best.call || best.callPath == nil && r.call == best.call {
			best.call = r.call
			best.callPath = r.callPath
		}
		if r.data > best.data || best.dataPath == nil && r.data == best.data {
			best.data = r.data
			best.dataPath = r.dataPath
		}
	}
	res := &result{
		call:     own + best.call,
		data:     fn.DataFrame + best.data,
		callPath: append([]Frame{{fn, own}}, best.callPath...),
		dataPath: append([]Frame{{fn, fn.DataFrame}}, best.dataPath...),
	}
	a.memo[fn] = res
	return res
}

// reach returns the functions reachable from fn, fn first.
func reach(fn *types.Fn) []*types.Fn {
	seen := map[*types.Fn]bool{}
	var out []*types.Fn
	var visit func(f *types.Fn)
	visit = func(f *types.Fn) {
		if seen[f] {
			return
		}
		seen[f] = true
		out = append(out, f)
		for _, c := range f.Calls {
			visit(c.Callee)
		}
	}
	visit(fn)
	return out
}

func entryName(fn *types.Fn) string {
	if fn.ISR {
		return "isr " + fn.Name
	}
	return fn.Name
}

// shared enforces E204 (globals touched from more than one entry point)
// and E205 (queues with more than one producer or consumer entry).
func (a *analyzer) shared() {
	type use struct {
		entry *types.Fn
		pos   diag.Pos
	}
	globals := map[*types.Symbol][]use{}
	senders := map[*types.Symbol][]use{}
	receivers := map[*types.Symbol][]use{}
	for _, e := range a.p.Entries {
		gseen := map[*types.Symbol]bool{}
		sseen := map[*types.Symbol]bool{}
		rseen := map[*types.Symbol]bool{}
		for _, fn := range reach(e) {
			for sym, u := range fn.Globals {
				if sym.Type != nil && sym.Type.Kind == types.Queue {
					continue
				}
				if !gseen[sym] {
					gseen[sym] = true
					globals[sym] = append(globals[sym], use{e, u.Pos})
				}
			}
			for sym, q := range fn.QueueUses {
				if q.Send && !sseen[sym] {
					sseen[sym] = true
					senders[sym] = append(senders[sym], use{e, q.SendPos})
				}
				if q.Recv && !rseen[sym] {
					rseen[sym] = true
					receivers[sym] = append(receivers[sym], use{e, q.RecvPos})
				}
			}
		}
	}
	for _, sym := range a.p.Globals {
		us := globals[sym]
		if len(us) > 1 {
			var names []string
			for _, u := range us {
				names = append(names, entryName(u.entry))
			}
			for _, u := range us[1:] {
				a.d.ErrorFix(u.pos, "E204", fmt.Sprintf("global `%s` is used by %s", sym.Name, strings.Join(names, " and ")),
					"hand values across with a `queue`, or keep the global inside one entry's call tree")
			}
		}
		for _, roles := range []struct {
			us   []use
			verb string
		}{{senders[sym], "sent to"}, {receivers[sym], "received from"}} {
			if len(roles.us) > 1 {
				var names []string
				for _, u := range roles.us {
					names = append(names, entryName(u.entry))
				}
				for _, u := range roles.us[1:] {
					a.d.ErrorFix(u.pos, "E205", fmt.Sprintf("queue `%s` is %s by %s", sym.Name, roles.verb, strings.Join(names, " and ")),
						"use one queue per producer/consumer pair")
				}
			}
		}
	}
}

func pathString(fs []Frame) string {
	var names []string
	for _, f := range fs {
		names = append(names, f.Fn.Name)
	}
	return strings.Join(names, ">")
}

// Line renders the one-line summary:
//
//	STACK main=412B isr USART2_IRQHandler=96B data=1088B total=1596B of 2048B src=est
func (r *Report) Line() string {
	if !r.Valid {
		return "STACK unbounded (see errors)"
	}
	var b strings.Builder
	b.WriteString("STACK")
	for _, e := range r.Entries {
		name := e.Fn.Name
		if e.Fn.ISR {
			name = "isr:" + name
		}
		fmt.Fprintf(&b, " %s=%dB", name, e.Call)
	}
	fmt.Fprintf(&b, " data=%dB total=%dB", r.DataTotal, r.Total)
	if r.Budget > 0 {
		fmt.Fprintf(&b, " of %dB", r.Budget)
	}
	fmt.Fprintf(&b, " src=%s", r.Source)
	return b.String()
}

// ChecksLine renders the runtime-check summary.
func (r *Report) ChecksLine() string {
	if r.NChecks == 0 {
		return "CHECKS runtime=0"
	}
	var kinds []string
	for k := range r.Checks {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var parts []string
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s=%d", k, r.Checks[k]))
	}
	return fmt.Sprintf("CHECKS runtime=%d %s", r.NChecks, strings.Join(parts, " "))
}

// Detail renders per-entry worst-case paths.
func (r *Report) Detail() string {
	var b strings.Builder
	for _, e := range r.Entries {
		fmt.Fprintf(&b, "%s call=%dB", entryName(e.Fn), e.Call)
		if e.Fn.ISR {
			fmt.Fprintf(&b, " (incl. exception frame)")
		}
		b.WriteString("\n  path")
		for _, f := range e.CallPath {
			fmt.Fprintf(&b, " %s:%d", f.Fn.Name, f.Bytes)
		}
		fmt.Fprintf(&b, "\n%s data=%dB\n  path", entryName(e.Fn), e.Data)
		for _, f := range e.DataPath {
			fmt.Fprintf(&b, " %s:%d", f.Fn.Name, f.Bytes)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ReadSU parses gcc -fstack-usage files (or directories containing them).
// Each line is `file:line:col:function<TAB>bytes<TAB>qualifiers`.
// Functions with dynamic frames are rejected: Quoin never emits them.
func ReadSU(paths []string) (map[string]int64, error) {
	out := map[string]int64{}
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			m, _ := filepath.Glob(filepath.Join(p, "*.su"))
			files = append(files, m...)
		} else {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .su files found in %s", strings.Join(paths, ", "))
	}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			parts := strings.Split(sc.Text(), "\t")
			if len(parts) < 3 {
				continue
			}
			loc := parts[0]
			name := loc[strings.LastIndexByte(loc, ':')+1:]
			n, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
			if err != nil {
				continue
			}
			if strings.Contains(parts[2], "dynamic") && !strings.Contains(parts[2], "bounded") {
				fh.Close()
				return nil, fmt.Errorf("%s: %s has a dynamic frame", f, name)
			}
			out[name] = max(out[name], n)
		}
		fh.Close()
	}
	return out, nil
}
