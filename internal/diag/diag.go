// Package diag holds Quoin's structured diagnostics.
//
// Every diagnostic is one line a model can parse without reading prose:
//
//	E107 uart.qn:12:5 UnhandledError send(...) returns !Timeout
//	  fix: append `?` to propagate, or `catch` to handle
//
// The code is stable, the kind is a CamelCase name for the code, the message
// is a terse fact, and the fix line (when present) is a concrete edit.
package diag

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Pos is a source position. Line and Col are 1-based.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Before reports whether p sorts before q.
func (p Pos) Before(q Pos) bool {
	if p.File != q.File {
		return p.File < q.File
	}
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Col < q.Col
}

// Severity of a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)

// Diag is one structured diagnostic.
type Diag struct {
	Code string   `json:"code"`
	Kind string   `json:"kind"`
	Pos  Pos      `json:"-"`
	Msg  string   `json:"msg"`
	Fix  string   `json:"fix,omitempty"`
	Sev  Severity `json:"-"`
}

// Format renders the diagnostic in the compact text form.
func (d Diag) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s", d.Code, d.Pos, d.Kind)
	if d.Msg != "" {
		b.WriteString(" ")
		b.WriteString(d.Msg)
	}
	if d.Fix != "" {
		b.WriteString("\n  fix: ")
		b.WriteString(d.Fix)
	}
	return b.String()
}

// MarshalJSON flattens the position so agents get one flat object.
func (d Diag) MarshalJSON() ([]byte, error) {
	sev := "error"
	if d.Sev == Warning {
		sev = "warning"
	}
	return json.Marshal(struct {
		Code     string `json:"code"`
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
		File     string `json:"file"`
		Line     int    `json:"line"`
		Col      int    `json:"col"`
		Msg      string `json:"msg"`
		Fix      string `json:"fix,omitempty"`
	}{d.Code, d.Kind, sev, d.Pos.File, d.Pos.Line, d.Pos.Col, d.Msg, d.Fix})
}

// List collects diagnostics.
type List struct {
	Items []Diag
	seen  map[string]bool
}

// Add records a diagnostic. The kind is looked up from the code table.
// Exact duplicates (same code, position and message) are dropped.
func (l *List) Add(sev Severity, pos Pos, code, msg, fix string) {
	key := code + pos.String() + msg
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	if l.seen[key] {
		return
	}
	l.seen[key] = true
	l.Items = append(l.Items, Diag{Code: code, Kind: KindOf(code), Pos: pos, Msg: msg, Fix: fix, Sev: sev})
}

// Errorf records an error with a formatted message and no fix.
func (l *List) Errorf(pos Pos, code, format string, args ...any) {
	l.Add(Error, pos, code, fmt.Sprintf(format, args...), "")
}

// ErrorFix records an error with a fix line.
func (l *List) ErrorFix(pos Pos, code, msg, fix string) {
	l.Add(Error, pos, code, msg, fix)
}

// Warn records a warning.
func (l *List) Warn(pos Pos, code, msg, fix string) {
	l.Add(Warning, pos, code, msg, fix)
}

// HasErrors reports whether any error-severity diagnostic was recorded.
func (l *List) HasErrors() bool {
	for _, d := range l.Items {
		if d.Sev == Error {
			return true
		}
	}
	return false
}

// Sort orders diagnostics by position.
func (l *List) Sort() {
	sort.SliceStable(l.Items, func(i, j int) bool {
		return l.Items[i].Pos.Before(l.Items[j].Pos)
	})
}

// Text renders all diagnostics, one per entry.
func (l *List) Text() string {
	var b strings.Builder
	for _, d := range l.Items {
		b.WriteString(d.Format())
		b.WriteString("\n")
	}
	return b.String()
}
