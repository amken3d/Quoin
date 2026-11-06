package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// hardTasks carry the traps that make firmware fail after it compiles:
// signed overflow, shifts by the full width, wrapping counters, overflow
// detection, framing overruns, stack-hungry buffers and signedness.
var hardTasks = []Task{
	{
		Name: "q15fir",
		Statement: `The input is signed 16-bit little-endian samples x[0], x[1], ... (ignore a trailing odd byte).
The coefficients are c = [-3000, 6000, 12000, 18000, 18000, 12000, 6000, -3000].
For each n from 7 to the last sample index:
  acc = 0, a signed 32-bit accumulator
  for k = 0..7: acc = acc + c[k] * x[n-k], saturating acc to [-2147483648, 2147483647] after every addition
  acc = acc + 16384, also saturating
  y = acc shifted right arithmetically by 15, then saturated to [-32768, 32767]
  print y`,
		StackBound: 1024,
		Edges: [][]byte{{}, le16s(32767, 32767, 32767, 32767, 32767, 32767, 32767, 32767, 32767),
			le16s(-32768, -32768, -32768, -32768, -32768, -32768, -32768, -32768), le16s(1, 2, 3, 4, 5, 6, 7), append(le16s(100, -100, 200, -200, 300, -300, 400, -400), 9)},
		Gen: func(r *rand.Rand) []byte {
			n := r.Intn(41)
			v := make([]int, n)
			for i := range v {
				switch r.Intn(10) {
				case 0:
					v[i] = 32767
				case 1:
					v[i] = -32768
				default:
					v[i] = r.Intn(65536) - 32768
				}
			}
			return le16s(v...)
		},
		Ref: func(in []byte) []string {
			c := []int64{-3000, 6000, 12000, 18000, 18000, 12000, 6000, -3000}
			var x []int64
			for i := 0; i+1 < len(in); i += 2 {
				x = append(x, int64(int16(binary.LittleEndian.Uint16(in[i:]))))
			}
			sat := func(v, lo, hi int64) int64 { return max(lo, min(hi, v)) }
			var lines []string
			for n := 7; n < len(x); n++ {
				var acc int64
				for k := 0; k < 8; k++ {
					acc = sat(acc+c[k]*x[n-k], -2147483648, 2147483647)
				}
				acc = sat(acc+16384, -2147483648, 2147483647)
				lines = append(lines, d(sat(acc>>15, -32768, 32767)))
			}
			return lines
		},
	},
	{
		Name: "cobs",
		Statement: `The input is a stream of COBS-encoded frames, each terminated by a 0x00 byte. Within a frame,
the bytes form blocks: a code byte c (1 to 255) followed by c-1 data bytes. The decoded frame is the
data bytes of every block in order, with one 0x00 inserted after each block whose code is below 255,
except after the frame's last block.
For each frame print two lines: the decoded length, then the XOR of all decoded bytes. If a block
would run past the frame's terminator, or the frame is empty (the terminator comes first), print -1
once for that frame and continue with the next frame. Ignore bytes after the last 0x00.`,
		StackBound: 1024,
		Edges: [][]byte{{}, {0}, {1, 0}, {3, 'a', 'b', 0}, {5, 1, 0}, {2, 7, 2, 9, 0, 9, 9},
			append(append([]byte{0xFF}, nonzero(254)...), 1, 0)},
		Gen: func(r *rand.Rand) []byte {
			var out []byte
			for f := r.Intn(5); f > 0; f-- {
				data := make([]byte, r.Intn(300))
				for i := range data {
					if r.Intn(5) > 0 {
						data[i] = byte(1 + r.Intn(255))
					}
				}
				enc := cobsEncode(data)
				if r.Intn(8) == 0 && len(enc) > 1 {
					enc[r.Intn(len(enc))] = byte(200 + r.Intn(56))
				}
				out = append(append(out, enc...), 0)
			}
			if r.Intn(8) == 0 {
				out = append(out, 0)
			}
			if r.Intn(6) == 0 {
				out = append(out, 3, 1, 2)
			}
			return out
		},
		Ref: func(in []byte) []string {
			var lines []string
			start := 0
			for i, b := range in {
				if b != 0 {
					continue
				}
				f := in[start:i]
				start = i + 1
				if len(f) == 0 {
					lines = append(lines, "-1")
					continue
				}
				var n, x int
				ok := true
				for j := 0; j < len(f); {
					c := int(f[j])
					if j+c > len(f) {
						ok = false
						break
					}
					for _, v := range f[j+1 : j+c] {
						n++
						x ^= int(v)
					}
					j += c
					if j < len(f) && c < 255 {
						n++
					}
				}
				if ok {
					lines = append(lines, d(int64(n)), d(int64(x)))
				} else {
					lines = append(lines, "-1")
				}
			}
			return lines
		},
	},
	{
		Name: "bitstream",
		Statement: `The input is a bitstream read most significant bit first: bit 7 of byte 0 is the first bit,
then bit 6, and so on. It holds records: a 5-bit field W, then a value of W+1 bits (1 to 32 bits),
both most significant bit first. For each record print the value as an unsigned number.
Stop when fewer than 5 bits remain. If a W field is read but fewer than W+1 bits remain after it,
print -1 and stop.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {0xF8}, bits(rec(31, 0xFFFFFFFF), rec(0, 1)), bits(rec(31, 0x80000001), rec(7, 0xA5), rec(15, 0xBEEF))},
		Gen: func(r *rand.Rand) []byte {
			var rs [][2]uint64
			for n := r.Intn(8); n > 0; n-- {
				w := uint64(r.Intn(32))
				if r.Intn(3) == 0 {
					w = 31
				}
				v := r.Uint64() & (1<<(w+1) - 1)
				rs = append(rs, rec(w, v))
			}
			b := bits(rs...)
			if r.Intn(6) == 0 && len(b) > 0 {
				b = b[:len(b)-1]
			}
			return b
		},
		Ref: func(in []byte) []string {
			var lines []string
			total := len(in) * 8
			pos := 0
			read := func(n int) uint64 {
				var v uint64
				for i := 0; i < n; i++ {
					bit := in[pos/8] >> (7 - pos%8) & 1
					v = v<<1 | uint64(bit)
					pos++
				}
				return v
			}
			for total-pos >= 5 {
				w := int(read(5))
				if total-pos < w+1 {
					return append(lines, "-1")
				}
				lines = append(lines, fmt.Sprint(read(w+1)))
			}
			return lines
		},
	},
	{
		Name: "isqrt",
		Statement: `The input is unsigned 32-bit little-endian values (ignore trailing bytes that do not form a full
value). For each value print floor(sqrt(value)), computed exactly with integer arithmetic.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, le32s(0, 1, 2, 3, 4), le32s(4294967295, 4294836225, 4294836224, 65536*65535), append(le32s(99), 1, 2, 3)},
		Gen: func(r *rand.Rand) []byte {
			var v []uint32
			for n := r.Intn(12); n > 0; n-- {
				switch r.Intn(4) {
				case 0:
					s := uint32(r.Intn(65536))
					v = append(v, s*s+uint32(r.Intn(3))-1)
				case 1:
					v = append(v, uint32(r.Intn(1000)))
				default:
					v = append(v, r.Uint32())
				}
			}
			return le32s(v...)
		},
		Ref: func(in []byte) []string {
			var lines []string
			for i := 0; i+3 < len(in); i += 4 {
				v := uint64(binary.LittleEndian.Uint32(in[i:]))
				lo, hi := uint64(0), uint64(65536)
				for lo+1 < hi {
					m := (lo + hi) / 2
					if m*m <= v {
						lo = m
					} else {
						hi = m
					}
				}
				lines = append(lines, fmt.Sprint(lo))
			}
			return lines
		},
	},
	{
		Name: "tsdelta",
		Statement: `The input is unsigned 32-bit little-endian timestamps from a free-running microsecond counter
that wraps from 0xFFFFFFFF to 0 (ignore trailing bytes that do not form a full value).
For each consecutive pair, print the elapsed time: the later minus the earlier, modulo 2^32.
After that, print how many elapsed times exceeded 1000000, then the largest elapsed time
(0 if there were no pairs).`,
		StackBound: 1024,
		Edges:      [][]byte{{}, le32s(7), le32s(0xFFFFFFF0, 0x10), le32s(5, 1000005, 2000006, 2000006), le32s(0, 0xFFFFFFFF, 0)},
		Gen: func(r *rand.Rand) []byte {
			t := r.Uint32()
			if r.Intn(2) == 0 {
				t = 0xFFFFFFFF - uint32(r.Intn(3000000))
			}
			v := []uint32{t}
			for n := r.Intn(12); n > 0; n-- {
				switch r.Intn(4) {
				case 0:
					t += uint32(999990 + r.Intn(20))
				case 1:
					t += r.Uint32()
				default:
					t += uint32(r.Intn(5000))
				}
				v = append(v, t)
			}
			return le32s(v...)
		},
		Ref: func(in []byte) []string {
			var lines []string
			var ts []uint32
			for i := 0; i+3 < len(in); i += 4 {
				ts = append(ts, binary.LittleEndian.Uint32(in[i:]))
			}
			var over, big uint32
			for i := 1; i < len(ts); i++ {
				e := ts[i] - ts[i-1]
				lines = append(lines, u(e))
				if e > 1000000 {
					over++
				}
				big = max(big, e)
			}
			return append(lines, u(over), u(big))
		},
	},
	{
		Name: "median",
		Statement: `The input is packets: a length byte N, then N payload bytes. For each packet print the median of
its payload bytes as unsigned values (for even N, the lower of the two middle values).
If N is 0, print -1 and stop. If the input ends inside a packet, print -2 and stop.
Packets can be up to 255 bytes long. The stack budget for this task is small: plan memory use accordingly.`,
		StackBound: 256,
		Edges:      [][]byte{{}, {0}, {1, 7}, {2, 9, 3}, {4, 5, 1, 5, 1}, append([]byte{255}, desc(255)...), {3, 1, 2}},
		Gen: func(r *rand.Rand) []byte {
			var out []byte
			for p := 1 + r.Intn(4); p > 0; p-- {
				n := 1 + r.Intn(255)
				out = append(out, byte(n))
				out = append(out, randBytes(r, n)...)
			}
			switch r.Intn(8) {
			case 0:
				out = append(out, 0, 5)
			case 1:
				out = append(out, 9, 1, 2)
			}
			return out
		},
		Ref: func(in []byte) []string {
			var lines []string
			for i := 0; i < len(in); {
				n := int(in[i])
				if n == 0 {
					return append(lines, "-1")
				}
				if i+1+n > len(in) {
					return append(lines, "-2")
				}
				p := append([]byte{}, in[i+1:i+1+n]...)
				sort.Slice(p, func(a, b int) bool { return p[a] < p[b] })
				lines = append(lines, d(int64(p[(n-1)/2])))
				i += 1 + n
			}
			return lines
		},
	},
	{
		Name: "atoi",
		Statement: `The input is ASCII text: unsigned decimal numbers separated by one or more spaces or commas
(separators may also lead or trail). Print each number. Leading zeros are allowed. If a number is
greater than 4294967295, print -1 for it instead. If any other character appears, print -2 and stop
(numbers completed before it are still printed).`,
		StackBound: 1024,
		Edges: [][]byte{[]byte(""), []byte("0"), []byte(" 4294967295,4294967296 , 007"), []byte("99999999999 12"), []byte("12 3x 4"),
			[]byte("4294967290,4294967300,42949672950")},
		Gen: func(r *rand.Rand) []byte {
			var b strings.Builder
			for n := r.Intn(8); n > 0; n-- {
				b.WriteString([]string{" ", ",", "  ", ", ", ""}[r.Intn(4)])
				switch r.Intn(5) {
				case 0:
					b.WriteString(fmt.Sprint(uint64(4294967290 + r.Intn(12))))
				case 1:
					b.WriteString(fmt.Sprint(r.Uint64() % 100000000000))
				case 2:
					b.WriteString("00" + fmt.Sprint(r.Intn(100)))
				default:
					b.WriteString(fmt.Sprint(r.Uint32()))
				}
				b.WriteString([]string{" ", ","}[r.Intn(2)])
			}
			if r.Intn(6) == 0 {
				b.WriteString("7a 1")
			}
			return []byte(b.String())
		},
		Ref: func(in []byte) []string {
			var lines []string
			var v uint64
			in2 := append(append([]byte{}, in...), ' ')
			inNum, over := false, false
			for i, c := range in2 {
				switch {
				case c >= '0' && c <= '9':
					inNum = true
					if !over {
						v = v*10 + uint64(c-'0')
						if v > 4294967295 {
							over = true
						}
					}
				case c == ' ' || c == ',':
					if inNum {
						if over {
							lines = append(lines, "-1")
						} else {
							lines = append(lines, fmt.Sprint(v))
						}
					}
					inNum, over, v = false, false, 0
				default:
					_ = i
					return append(lines, "-2")
				}
			}
			return lines
		},
	},
	{
		Name: "median5",
		Statement: `Treat the input bytes as signed 8-bit samples (two's complement). For every position i >= 4,
print the median of samples i-4, i-3, i-2, i-1 and i.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {1, 2, 3, 4}, {0x80, 0x7F, 0, 1, 0xFF}, {0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0x05}},
		Gen:        func(r *rand.Rand) []byte { return randBytes(r, r.Intn(50)) },
		Ref: func(in []byte) []string {
			var lines []string
			for i := 4; i < len(in); i++ {
				w := []int{}
				for _, b := range in[i-4 : i+1] {
					w = append(w, int(int8(b)))
				}
				sort.Ints(w)
				lines = append(lines, d(int64(w[2])))
			}
			return lines
		},
	},
}

func le16s(v ...int) []byte {
	b := make([]byte, 2*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(x)))
	}
	return b
}

func le32s(v ...uint32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], x)
	}
	return b
}

func nonzero(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(1 + i%255)
	}
	return b
}

func desc(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(n - i)
	}
	return b
}

func cobsEncode(data []byte) []byte {
	out := []byte{0}
	ci, code := 0, byte(1)
	for _, b := range data {
		if b == 0 {
			out[ci] = code
			ci, code = len(out), 1
			out = append(out, 0)
			continue
		}
		out = append(out, b)
		code++
		if code == 0xFF {
			out[ci] = code
			ci, code = len(out), 1
			out = append(out, 0)
		}
	}
	out[ci] = code
	return out
}

func rec(w, v uint64) [2]uint64 { return [2]uint64{w, v} }

// bits packs records (5-bit W, then W+1 value bits) MSB first, zero padded.
func bits(rs ...[2]uint64) []byte {
	var out []byte
	var acc uint64
	n := 0
	put := func(v uint64, w int) {
		for i := w - 1; i >= 0; i-- {
			acc = acc<<1 | (v>>uint(i))&1
			n++
			if n == 8 {
				out = append(out, byte(acc))
				acc, n = 0, 0
			}
		}
	}
	for _, r := range rs {
		put(r[0], 5)
		put(r[1], int(r[0])+1)
	}
	if n > 0 {
		out = append(out, byte(acc<<(8-n)))
	}
	return out
}
