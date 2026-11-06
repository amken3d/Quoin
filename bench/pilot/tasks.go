package main

import (
	"fmt"
	"math/rand"
)

// Task is one pilot task. The statement is identical for every language;
// Gen draws a fresh input for every test run (so programs cannot hardcode
// answers) and Ref computes the expected output lines.
type Task struct {
	Name       string
	Statement  string
	StackBound int64    // bytes; measured worst case must not exceed this
	Edges      [][]byte // fixed edge cases, run on every test before random inputs
	Gen        func(r *rand.Rand) []byte
	Ref        func(in []byte) []string
}

func d(v int64) string  { return fmt.Sprint(v) }
func u(v uint32) string { return fmt.Sprint(v) }

func longRun(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func randBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	return b
}

var tasks = []Task{
	{
		Name: "crc16",
		Statement: `Compute CRC-16/CCITT-FALSE over all input bytes: polynomial 0x1021, initial value 0xFFFF,
input and output not reflected, no final XOR. Process each byte most significant bit first.
Print the CRC as one unsigned number.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39}},
		Gen:        func(r *rand.Rand) []byte { return randBytes(r, r.Intn(200)) },
		Ref: func(in []byte) []string {
			crc := uint32(0xFFFF)
			for _, b := range in {
				crc ^= uint32(b) << 8
				for i := 0; i < 8; i++ {
					if crc&0x8000 != 0 {
						crc = (crc<<1 ^ 0x1021) & 0xFFFF
					} else {
						crc = crc << 1 & 0xFFFF
					}
				}
			}
			return []string{u(crc)}
		},
	},
	{
		Name: "packets",
		Statement: `The input is a sequence of packets. Each packet is one length byte N (0 to 32), then N payload
bytes, then one checksum byte equal to the XOR of the payload bytes (0 for an empty payload).
For each packet, print the sum of its payload bytes if the checksum matches, or -1 if it does not.
If a length byte is greater than 32, print -2 and stop. If the input ends in the middle of a
packet, print -3 and stop. Otherwise stop at the end of the input.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {0}, {3, 1, 2, 3, 0}, {2, 5, 6}, {40, 1, 2, 3}, {1, 7, 7, 33}},
		Gen: func(r *rand.Rand) []byte {
			var out []byte
			for p := r.Intn(7); p > 0; p-- {
				n := r.Intn(33)
				pl := randBytes(r, n)
				var x byte
				for _, b := range pl {
					x ^= b
				}
				if r.Intn(10) < 3 {
					x ^= byte(1 + r.Intn(255))
				}
				out = append(out, byte(n))
				out = append(out, pl...)
				out = append(out, x)
			}
			switch r.Intn(10) {
			case 0:
				out = append(out, byte(33+r.Intn(200)), 1, 2)
			case 1:
				if len(out) > 1 {
					out = out[:len(out)-1-r.Intn(len(out)/2+1)]
				}
			}
			return out
		},
		Ref: func(in []byte) []string {
			var lines []string
			i := 0
			for i < len(in) {
				n := int(in[i])
				if n > 32 {
					return append(lines, "-2")
				}
				if i+1+n+1 > len(in) {
					return append(lines, "-3")
				}
				var sum int64
				var x byte
				for _, b := range in[i+1 : i+1+n] {
					sum += int64(b)
					x ^= b
				}
				if x == in[i+1+n] {
					lines = append(lines, d(sum))
				} else {
					lines = append(lines, "-1")
				}
				i += n + 2
			}
			return lines
		},
	},
	{
		Name: "ringbuf",
		Statement: `Simulate a FIFO queue of bytes with capacity 8, initially empty. Each input byte is a command:
a byte below 0x80 pushes its own value (if the queue is full, print -1 and drop the value);
a byte of 0x80 or above pops (print the popped value, or -2 if the queue is empty).
Print nothing for a successful push. After all commands, print the number of values left in the queue.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {0x80}, {1, 2, 3, 4, 5, 6, 7, 8, 9, 0x80, 0xFF, 10, 0x80}},
		Gen: func(r *rand.Rand) []byte {
			b := make([]byte, r.Intn(60))
			for i := range b {
				if r.Intn(10) < 6 {
					b[i] = byte(r.Intn(0x80))
				} else {
					b[i] = byte(0x80 + r.Intn(0x80))
				}
			}
			return b
		},
		Ref: func(in []byte) []string {
			var q []byte
			var lines []string
			for _, c := range in {
				if c < 0x80 {
					if len(q) == 8 {
						lines = append(lines, "-1")
					} else {
						q = append(q, c)
					}
				} else if len(q) == 0 {
					lines = append(lines, "-2")
				} else {
					lines = append(lines, d(int64(q[0])))
					q = q[1:]
				}
			}
			return append(lines, d(int64(len(q))))
		},
	},
	{
		Name: "debounce",
		Statement: `Each input byte is one sample of a switch; the sample is 1 when the byte's lowest bit is 1, else 0.
The debounced state starts at 0. It changes only after 3 consecutive samples all differ from the
current debounced state; the change takes effect at the third such sample, and counting starts
again after a change. For every change print two lines: the 0-based index of the sample at which
it took effect, then the new state (0 or 1).`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {1, 1, 1, 0, 0, 1, 1, 1, 0, 0, 0}, {3, 5, 7, 9, 11, 13}},
		Gen: func(r *rand.Rand) []byte {
			b := make([]byte, r.Intn(80))
			v := 0
			for i := range b {
				if r.Intn(10) < 3 {
					v ^= 1
				}
				b[i] = byte(r.Intn(128)<<1 | v)
			}
			return b
		},
		Ref: func(in []byte) []string {
			var lines []string
			state, run := 0, 0
			for i, b := range in {
				s := int(b & 1)
				if s != state {
					run++
					if run == 3 {
						state, run = s, 0
						lines = append(lines, d(int64(i)), d(int64(state)))
					}
				} else {
					run = 0
				}
			}
			return lines
		},
	},
	{
		Name: "movavg",
		Statement: `Treat the input bytes as unsigned samples. For every position i >= 3, print the average of
samples i-3, i-2, i-1 and i, rounded down. Print nothing if there are fewer than 4 samples.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {1, 2, 3}, {255, 255, 255, 255, 0}},
		Gen:        func(r *rand.Rand) []byte { return randBytes(r, r.Intn(50)) },
		Ref: func(in []byte) []string {
			var lines []string
			for i := 3; i < len(in); i++ {
				s := int64(in[i-3]) + int64(in[i-2]) + int64(in[i-1]) + int64(in[i])
				lines = append(lines, d(s/4))
			}
			return lines
		},
	},
	{
		Name: "pid",
		Statement: `The input bytes come in pairs (setpoint, measurement), both unsigned. Start with integral = 0
and previous error = 0. For each pair:
  error = setpoint - measurement
  integral = integral + error, then clamp integral to [-1000, 1000]
  output = 3*error + integral/4 + 2*(error - previous error), where integral/4 rounds toward zero
  clamp output to [-255, 255] and print it
  previous error = error
Ignore a trailing unpaired byte.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {7}, {255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 0, 255, 0, 255}},
		Gen:        func(r *rand.Rand) []byte { return randBytes(r, r.Intn(61)) },
		Ref: func(in []byte) []string {
			var lines []string
			var integ, prev int64
			for i := 0; i+1 < len(in); i += 2 {
				e := int64(in[i]) - int64(in[i+1])
				integ = max(-1000, min(1000, integ+e))
				out := 3*e + integ/4 + 2*(e-prev)
				out = max(-255, min(255, out))
				lines = append(lines, d(out))
				prev = e
			}
			return lines
		},
	},
	{
		Name: "ctrlreg",
		Statement: `A 32-bit memory-mapped control register is at address 0x10370000. Its fields are
EN = bit 0, MODE = bits 4-5, DIV = bits 8-19, IRQ = bit 31; all other bits must stay 0.
First write 0 to the register. The input is then a sequence of (field, value) byte pairs, where
field 0 = EN, 1 = MODE, 2 = DIV, 3 = IRQ; for DIV the number to store is value * 16.
If the field number is not 0-3, or the number does not fit in the field, print -1 and leave the
register unchanged. Otherwise update only that field with a read-modify-write of the register and
print the register's new value as an unsigned number. Ignore a trailing unpaired byte.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {0, 1, 1, 3, 2, 255, 3, 1, 1, 4, 0, 2, 4, 0, 3, 0, 9}},
		Gen: func(r *rand.Rand) []byte {
			b := make([]byte, 2*r.Intn(25))
			for i := 0; i < len(b); i += 2 {
				b[i] = byte(r.Intn(5))
				switch r.Intn(3) {
				case 0:
					b[i+1] = byte(r.Intn(256))
				default:
					b[i+1] = byte(r.Intn(4))
				}
			}
			if r.Intn(4) == 0 {
				b = append(b, byte(r.Intn(4)))
			}
			return b
		},
		Ref: func(in []byte) []string {
			var lines []string
			var reg uint32
			for i := 0; i+1 < len(in); i += 2 {
				f, v := in[i], uint32(in[i+1])
				var shift, width uint32
				switch f {
				case 0:
					shift, width = 0, 1
				case 1:
					shift, width = 4, 2
				case 2:
					shift, width, v = 8, 12, v*16
				case 3:
					shift, width = 31, 1
				default:
					lines = append(lines, "-1")
					continue
				}
				if v >= 1<<width {
					lines = append(lines, "-1")
					continue
				}
				mask := uint32(1<<width-1) << shift
				reg = reg&^mask | v<<shift
				lines = append(lines, u(reg))
			}
			return lines
		},
	},
	{
		Name: "mix",
		Statement: `The input is interleaved signed 8-bit samples (two's complement) for two channels:
ch1, ch2, ch1, ch2, ... For each pair, scale both samples to 16 bits by multiplying by 200,
add them, saturate the sum to the signed 16-bit range [-32768, 32767] and print it.
Ignore a trailing unpaired byte.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {127, 127, 0x80, 0x80, 127, 0x80, 1}},
		Gen:        func(r *rand.Rand) []byte { return randBytes(r, r.Intn(61)) },
		Ref: func(in []byte) []string {
			var lines []string
			for i := 0; i+1 < len(in); i += 2 {
				s := int64(int8(in[i]))*200 + int64(int8(in[i+1]))*200
				lines = append(lines, d(max(-32768, min(32767, s))))
			}
			return lines
		},
	},
	{
		Name: "varint",
		Statement: `The input is a sequence of unsigned LEB128 varints: each byte carries 7 data bits, least
significant group first, and every byte except the last of a varint has its high bit (0x80) set.
Print each decoded value as an unsigned number. If a varint would need more than 5 bytes, or its
value does not fit in 32 bits, print -1 and stop. If the input ends inside a varint, print -2 and stop.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {0}, {0xFF, 0xFF, 0xFF, 0xFF, 0x0F}, {0xFF, 0xFF, 0xFF, 0xFF, 0x10}, {0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, {0x96, 0x01, 0x80}},
		Gen: func(r *rand.Rand) []byte {
			var out []byte
			for n := r.Intn(8); n > 0; n-- {
				var v uint32
				switch r.Intn(4) {
				case 0:
					v = uint32(r.Intn(128))
				case 1:
					v = uint32(r.Intn(1 << 14))
				default:
					v = r.Uint32()
				}
				for {
					b := byte(v & 0x7F)
					v >>= 7
					if v != 0 {
						out = append(out, b|0x80)
					} else {
						out = append(out, b)
						break
					}
				}
			}
			switch r.Intn(8) {
			case 0:
				out = append(out, 0xFF, 0xFF, 0xFF, 0xFF, byte(0x10+r.Intn(0x70)))
			case 1:
				out = append(out, 0x80, 0x80, 0x80, 0x80, 0x81, 0x01)
			case 2:
				out = append(out, 0x81, 0x82)
			}
			return out
		},
		Ref: func(in []byte) []string {
			var lines []string
			i := 0
			for i < len(in) {
				var v uint64
				k := 0
				for {
					if i >= len(in) {
						return append(lines, "-2")
					}
					b := in[i]
					i++
					if k == 4 && (b&0x80 != 0 || b > 0x0F) {
						return append(lines, "-1")
					}
					v |= uint64(b&0x7F) << (7 * k)
					k++
					if b&0x80 == 0 {
						break
					}
				}
				lines = append(lines, fmt.Sprint(v))
			}
			return lines
		},
	},
	{
		Name: "rle",
		Statement: `Run-length encode the input: for each maximal run of equal bytes print two lines, the run length
and the byte value. Runs longer than 255 are split into consecutive runs of at most 255.`,
		StackBound: 1024,
		Edges:      [][]byte{{}, {9}, longRun(300, 7)},
		Gen: func(r *rand.Rand) []byte {
			var out []byte
			for n := r.Intn(10); n > 0; n-- {
				run := 1 + r.Intn(4)
				if r.Intn(6) == 0 {
					run = 200 + r.Intn(200)
				}
				v := byte(r.Intn(4))
				for j := 0; j < run; j++ {
					out = append(out, v)
				}
			}
			return out
		},
		Ref: func(in []byte) []string {
			var lines []string
			for i := 0; i < len(in); {
				j := i
				for j < len(in) && in[j] == in[i] && j-i < 255 {
					j++
				}
				lines = append(lines, d(int64(j-i)), d(int64(in[i])))
				i = j
			}
			return lines
		},
	},
}
