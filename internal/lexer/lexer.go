// Package lexer turns Quoin source into tokens, including the INDENT,
// DEDENT and NEWLINE tokens that make indentation significant.
package lexer

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/amken3d/quoin/internal/diag"
)

// Kind is a token class.
type Kind int

const (
	EOF Kind = iota
	Newline
	Indent
	Dedent
	Ident
	Int
	Keyword
	Op
)

func (k Kind) String() string {
	switch k {
	case EOF:
		return "end of file"
	case Newline:
		return "newline"
	case Indent:
		return "indent"
	case Dedent:
		return "dedent"
	case Ident:
		return "identifier"
	case Int:
		return "integer"
	case Keyword:
		return "keyword"
	case Op:
		return "operator"
	}
	return "?"
}

// Token is one lexical token.
type Token struct {
	Kind Kind
	Text string
	Val  *big.Int // for Int (including character literals)
	Pos  diag.Pos
	// LineStart is true for the first real token on a source line.
	LineStart bool
}

func (t Token) String() string {
	switch t.Kind {
	case Ident, Keyword, Op, Int:
		return "`" + t.Text + "`"
	}
	return t.Kind.String()
}

// Keywords is the full reserved word list.
var Keywords = map[string]bool{
	"fn": true, "isr": true, "extern": true, "const": true, "var": true,
	"error": true, "enum": true, "struct": true, "regs": true, "periph": true,
	"if": true, "else": true, "for": true, "in": true, "break": true,
	"continue": true, "return": true, "catch": true, "where": true,
	"grow": true, "queue": true, "stack": true, "true": true, "false": true,
}

// Operators, longest first so greedy matching works.
var ops = []string{
	"<<=", ">>=", "+%=", "-%=", "*%=", "+|=", "-|=", "*|=",
	"+%", "-%", "*%", "+|", "-|", "*|",
	"==", "!=", "<=", ">=", "&&", "||", "<<", ">>", ":=", "+=", "-=", "*=",
	"/=", "%=", "&=", "|=", "^=", "..",
	"+", "-", "*", "/", "%", "&", "|", "^", "~", "!", "<", ">", "=", "(",
	")", "[", "]", "{", "}", ",", ":", ".", "?", "@",
}

type lexer struct {
	src    []byte
	file   string
	i      int
	line   int
	col    int
	toks   []Token
	indent []int
	depth  int // bracket nesting; newlines are ignored inside brackets
	open   []Token
	diags  *diag.List
}

// Lex tokenizes src. Errors are appended to diags; lexing always produces a
// token stream ending in EOF.
func Lex(file string, src []byte, diags *diag.List) []Token {
	lx := &lexer{src: src, file: file, line: 1, col: 1, indent: []int{0}, diags: diags}
	lx.run()
	return lx.toks
}

func (lx *lexer) pos() diag.Pos { return diag.Pos{File: lx.file, Line: lx.line, Col: lx.col} }

func (lx *lexer) peek(off int) byte {
	if lx.i+off < len(lx.src) {
		return lx.src[lx.i+off]
	}
	return 0
}

func (lx *lexer) advance() byte {
	c := lx.src[lx.i]
	lx.i++
	if c == '\n' {
		lx.line++
		lx.col = 1
	} else {
		lx.col++
	}
	return c
}

func (lx *lexer) emit(t Token) { lx.toks = append(lx.toks, t) }

func (lx *lexer) lastKind() Kind {
	if len(lx.toks) == 0 {
		return Newline
	}
	return lx.toks[len(lx.toks)-1].Kind
}

func (lx *lexer) run() {
	atLineStart := true
	for {
		if atLineStart && lx.depth == 0 {
			if !lx.handleIndent() {
				break // EOF
			}
			atLineStart = false
		}
		if lx.i >= len(lx.src) {
			break
		}
		c := lx.peek(0)
		switch {
		case c == '\n':
			lx.advance()
			if lx.depth == 0 {
				if k := lx.lastKind(); k != Newline && k != Indent && k != Dedent {
					lx.emit(Token{Kind: Newline, Pos: lx.pos()})
				}
				atLineStart = true
			}
		case c == ' ' || c == '\t' || c == '\r':
			lx.advance()
		case c == '/' && lx.peek(1) == '/':
			for lx.i < len(lx.src) && lx.peek(0) != '\n' {
				lx.advance()
			}
		default:
			lx.token(atLineStart)
		}
	}
	if k := lx.lastKind(); k != Newline && k != Indent && k != Dedent && len(lx.toks) > 0 {
		lx.emit(Token{Kind: Newline, Pos: lx.pos()})
	}
	for len(lx.indent) > 1 {
		lx.indent = lx.indent[:len(lx.indent)-1]
		lx.emit(Token{Kind: Dedent, Pos: lx.pos()})
	}
	eof := Token{Kind: EOF, Pos: lx.pos()}
	if len(lx.open) > 0 {
		o := lx.open[len(lx.open)-1]
		closer := map[string]string{"(": ")", "[": "]", "{": "}"}[o.Text]
		lx.diags.ErrorFix(o.Pos, "E003", "unclosed `"+o.Text+"`", "close it with `"+closer+"`")
		eof.Text = "unclosed" // the parser suppresses its own end-of-file cascade
	}
	lx.emit(eof)
}

// handleIndent measures the indentation of the next non-blank line and emits
// INDENT/DEDENT tokens. It returns false at end of input.
func (lx *lexer) handleIndent() bool {
	for {
		width := 0
		start := lx.pos()
		sawTab := false
		for lx.i < len(lx.src) && (lx.peek(0) == ' ' || lx.peek(0) == '\t') {
			if lx.peek(0) == '\t' {
				sawTab = true
			}
			lx.advance()
			width++
		}
		if lx.i >= len(lx.src) {
			return false
		}
		c := lx.peek(0)
		if c == '\r' {
			lx.advance()
			c = lx.peek(0)
		}
		if c == '\n' {
			lx.advance()
			continue
		}
		if c == '/' && lx.peek(1) == '/' {
			for lx.i < len(lx.src) && lx.peek(0) != '\n' {
				lx.advance()
			}
			continue
		}
		if sawTab {
			lx.diags.ErrorFix(start, "E002", "tab in indentation", "indent with spaces")
		}
		top := lx.indent[len(lx.indent)-1]
		switch {
		case width > top:
			lx.indent = append(lx.indent, width)
			lx.emit(Token{Kind: Indent, Pos: lx.pos()})
		case width < top:
			for len(lx.indent) > 1 && lx.indent[len(lx.indent)-1] > width {
				lx.indent = lx.indent[:len(lx.indent)-1]
				lx.emit(Token{Kind: Dedent, Pos: lx.pos()})
			}
			if lx.indent[len(lx.indent)-1] != width {
				lx.diags.ErrorFix(lx.pos(), "E002",
					fmt.Sprintf("dedent to column %d matches no enclosing block", width+1),
					fmt.Sprintf("indent to column %d", lx.indent[len(lx.indent)-1]+1))
			}
		}
		return true
	}
}

func isLetter(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

func (lx *lexer) token(lineStart bool) {
	p := lx.pos()
	// LineStart: first token after a newline (or the file start).
	first := lineStart || lx.lastKind() == Newline || lx.lastKind() == Indent || lx.lastKind() == Dedent
	if lx.depth > 0 {
		first = false
	}
	c := lx.peek(0)
	switch {
	case isLetter(c):
		start := lx.i
		for lx.i < len(lx.src) && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
			lx.advance()
		}
		text := string(lx.src[start:lx.i])
		k := Ident
		if Keywords[text] {
			k = Keyword
		}
		lx.emit(Token{Kind: k, Text: text, Pos: p, LineStart: first})
	case isDigit(c):
		lx.number(p, first)
	case c == '\'':
		lx.char(p, first)
	default:
		for _, op := range ops {
			if strings.HasPrefix(string(lx.src[lx.i:min(lx.i+len(op), len(lx.src))]), op) {
				for range op {
					lx.advance()
				}
				switch op {
				case "(", "[", "{":
					lx.depth++
					lx.open = append(lx.open, Token{Kind: Op, Text: op, Pos: p})
				case ")", "]", "}":
					if lx.depth > 0 {
						lx.depth--
						lx.open = lx.open[:len(lx.open)-1]
					}
				}
				lx.emit(Token{Kind: Op, Text: op, Pos: p, LineStart: first})
				return
			}
		}
		lx.advance()
		fix := ""
		if c == '"' {
			fix = "string literals are not in v0; use an array literal like ['h', 'i']"
		}
		lx.diags.Add(diag.Error, p, "E001", fmt.Sprintf("unexpected character %q", c), fix)
	}
}

func (lx *lexer) number(p diag.Pos, first bool) {
	start := lx.i
	base := 10
	if lx.peek(0) == '0' && (lx.peek(1) == 'x' || lx.peek(1) == 'X') {
		base = 16
		lx.advance()
		lx.advance()
	} else if lx.peek(0) == '0' && (lx.peek(1) == 'b' || lx.peek(1) == 'B') {
		base = 2
		lx.advance()
		lx.advance()
	} else if lx.peek(0) == '0' && (lx.peek(1) == 'o' || lx.peek(1) == 'O') {
		base = 8
		lx.advance()
		lx.advance()
	}
	digStart := lx.i
	for lx.i < len(lx.src) && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
		lx.advance()
	}
	text := string(lx.src[start:lx.i])
	digits := strings.ReplaceAll(string(lx.src[digStart:lx.i]), "_", "")
	v, ok := new(big.Int).SetString(digits, base)
	if !ok || digits == "" {
		lx.diags.Errorf(p, "E004", "malformed integer literal %s", text)
		v = big.NewInt(0)
	}
	lx.emit(Token{Kind: Int, Text: text, Val: v, Pos: p, LineStart: first})
}

func (lx *lexer) char(p diag.Pos, first bool) {
	start := lx.i
	lx.advance() // opening quote
	var v int64 = -1
	if lx.i < len(lx.src) && lx.peek(0) == '\\' {
		lx.advance()
		if lx.i < len(lx.src) {
			switch e := lx.advance(); e {
			case 'n':
				v = '\n'
			case 'r':
				v = '\r'
			case 't':
				v = '\t'
			case '0':
				v = 0
			case '\\', '\'', '"':
				v = int64(e)
			case 'x':
				if lx.i+1 < len(lx.src) {
					h := string(lx.src[lx.i : lx.i+2])
					if n, ok := new(big.Int).SetString(h, 16); ok {
						v = n.Int64()
						lx.advance()
						lx.advance()
					}
				}
			}
		}
	} else if lx.i < len(lx.src) && lx.peek(0) != '\'' && lx.peek(0) != '\n' && lx.peek(0) < 0x80 {
		v = int64(lx.advance())
	}
	if lx.i < len(lx.src) && lx.peek(0) == '\'' && v >= 0 {
		lx.advance()
	} else {
		for lx.i < len(lx.src) && lx.peek(0) != '\'' && lx.peek(0) != '\n' {
			lx.advance()
		}
		if lx.i < len(lx.src) && lx.peek(0) == '\'' {
			lx.advance()
		}
		lx.diags.Errorf(p, "E004", "malformed character literal %s", string(lx.src[start:lx.i]))
		v = 0
	}
	lx.emit(Token{Kind: Int, Text: string(lx.src[start:lx.i]), Val: big.NewInt(v), Pos: p, LineStart: first})
}
