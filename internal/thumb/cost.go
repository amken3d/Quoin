package thumb

import (
	"fmt"

	"github.com/amken3d/quoin/internal/diag"
)

// Timing is the per-instruction-class cycle model. Defaults are the worst
// case of each range in the Cortex-M4 instruction timing table (ARM DDI
// 0439), which the Cortex-M33's three-stage pipeline matches for the
// classes the backend emits. They assume code and data in zero-wait SRAM
// and no bus contention. They are NOT yet calibrated against DWT_CYCCNT on
// an RP2350; until they are, cycle bounds are model bounds.
type Timing struct {
	ALU     int64 // data processing, moves, compares, bitfield ops, IT
	Mul     int64 // MUL, MLA, MLS, UMULL, SMULL
	Div     int64 // UDIV, SDIV (2..12)
	Load    int64 // LDR family, single
	Store   int64 // STR family, single
	Taken   int64 // taken branch: 1 + pipeline refill (refill is 1..3)
	NotTkn  int64 // branch not taken
	Call    int64 // BL
	Ret     int64 // BX LR
	PopPC   int64 // extra for POP {..., pc} on top of 1 + n
	Barrier int64 // DMB
	// Extra wait states for device accesses by region. Device timing on
	// RP2350 must be measured; these are conservative placeholders.
	WaitSIO     int64 // 0xD0000000 single-cycle IO
	WaitAPB     int64 // 0x40000000..0x4FFFFFFF (APB bridge)
	WaitAHB     int64 // 0x50000000..0x5FFFFFFF
	WaitUnknown int64 // register block reached through a parameter
	ExcEntry    int64 // exception entry latency (no FPU context)
}

// DefaultTiming is the uncalibrated Cortex-M33 model.
var DefaultTiming = Timing{
	ALU: 1, Mul: 1, Div: 12, Load: 2, Store: 2, Taken: 4, NotTkn: 1, Call: 4, Ret: 4, PopPC: 3,
	Barrier: 4, WaitSIO: 0, WaitAPB: 4, WaitAHB: 2, WaitUnknown: 4, ExcEntry: 12,
}

// path is the cost of one class of paths: cycles and instructions.
// ok=false means there is no such path (negative infinity).
type path struct {
	cyc, ins int64
	ok       bool
}

var zero = path{ok: true}

func (a path) plus(b path) path {
	if !a.ok || !b.ok {
		return path{}
	}
	return path{a.cyc + b.cyc, a.ins + b.ins, true}
}

func maxp(a, b path) path {
	switch {
	case !a.ok:
		return b
	case !b.ok:
		return a
	}
	return path{max(a.cyc, b.cyc), max(a.ins, b.ins), true}
}

func (a path) times(n int64) path {
	if !a.ok {
		return a
	}
	return path{a.cyc * n, a.ins * n, true}
}

// unbounded records why a region has no static bound.
type unbounded struct {
	pos diag.Pos
	why string
}

// flow is the cost summary of a structured region, measured from its
// start: paths that fall through, and paths that leave through break,
// continue or return (up to and including the exit sequence).
type flow struct {
	fall, brk, cont, ret path
	unb                  *unbounded
}

func seq(a, b flow) flow {
	r := flow{
		fall: a.fall.plus(b.fall),
		brk:  maxp(a.brk, a.fall.plus(b.brk)),
		cont: maxp(a.cont, a.fall.plus(b.cont)),
		ret:  maxp(a.ret, a.fall.plus(b.ret)),
		unb:  a.unb,
	}
	if r.unb == nil && a.fall.ok {
		r.unb = b.unb
	}
	return r
}

func alt(a, b flow) flow {
	r := flow{fall: maxp(a.fall, b.fall), brk: maxp(a.brk, b.brk), cont: maxp(a.cont, b.cont), ret: maxp(a.ret, b.ret), unb: a.unb}
	if r.unb == nil {
		r.unb = b.unb
	}
	return r
}

// Bound is the worst-case result for one function.
type Bound struct {
	Cycles, Insns int64
	Unbounded     bool
	Why           string
	Pos           diag.Pos
	NeverReturns  bool
}

func (b Bound) String() string {
	switch {
	case b.Unbounded:
		return "unbounded"
	case b.NeverReturns:
		return "noreturn"
	}
	return fmt.Sprintf("%d", b.Cycles)
}
