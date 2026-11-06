package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestOracleRejects feeds deliberately broken programs through the real
// pipeline and checks that the oracle names each failure correctly. A
// benchmark whose oracle passes wrong programs measures nothing.
func TestOracleRejects(t *testing.T) {
	work := t.TempDir()
	tc, err := setupToolchain(work, "../..")
	if err != nil {
		t.Skip(err)
	}
	langs, err := tc.langs("../..")
	if err != nil {
		t.Fatal(err)
	}
	find := func(name string) *Task {
		for i := range tasks {
			if tasks[i].Name == name {
				return &tasks[i]
			}
		}
		t.Fatalf("no task %s", name)
		return nil
	}
	hdr := "#include <stdint.h>\nvoid emit(int32_t v);\nvoid emitu(uint32_t v);\nuint32_t input_len(void);\nuint8_t input_byte(uint32_t i);\n"
	qhdr := "extern fn emit(v: i32) stack 32 cycles 400\nextern fn emitu(v: u32) stack 32 cycles 400\nextern fn input_len() u32 stack 0 cycles 8\nextern fn input_byte(i: u32) u8 stack 0 cycles 16\n"
	cases := []struct {
		name, task, lang, src, want string
	}{
		{"rle without the 255 split", "rle", "c", hdr + `int main(void){uint32_t n=input_len(),i=0;while(i<n){uint8_t c=input_byte(i);uint32_t j=i;while(j<n&&input_byte(j)==c)j++;emitu(j-i);emitu(c);i=j;}return 0;}`, "wrong"},
		{"reads past the end of the input", "crc16", "c", hdr + `int main(void){emitu(input_byte(input_len()));return 0;}`, "trap"},
		{"never returns", "movavg", "c", hdr + `int main(void){for(;;){}}`, "timeout"},
		{"blows the stack budget", "crc16", "c", hdr + `int main(void){volatile uint8_t big[2048];for(int i=0;i<2048;i++)big[i]=(uint8_t)i;uint16_t crc=0xFFFF;uint32_t n=input_len();for(uint32_t i=0;i<n;i++){crc^=(uint16_t)(input_byte(i)<<8);for(int k=0;k<8;k++)crc=(crc&0x8000)?(uint16_t)((crc<<1)^0x1021):(uint16_t)(crc<<1);}emitu(crc+big[7]-7);return 0;}`, "stack"},
		{"faults on a bad address", "crc16", "c", hdr + `int main(void){*(volatile uint32_t*)0xFFFFFFF0u=1;return 0;}`, "fault"},
		{"Quoin overflow check traps", "movavg", "quoin", qhdr + "fn main()\n  n := input_len()\n  for i in 3..n\n    var s: u8 = input_byte(i)\n    s += input_byte(i - 1)\n    s += input_byte(i - 2)\n    s += input_byte(i - 3)\n    emitu(s / 4)\n", "trap"},
		{"Quoin wrong answer", "pid", "quoin", qhdr + "fn main()\n  emit(0)\n", "wrong"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lang := langs[c.lang]
			prov := &replayProvider{script: map[string][]string{c.task + "/" + c.lang + "/0": {"```\n" + c.src + "\n```"}}, calls: map[string]int{}}
			r := attempt(context.Background(), prov, tc, lang, find(c.task), 0, config{maxIters: 1, ceiling: 1 << 40, vectors: 3}, false)
			if len(r.Turns) != 1 {
				t.Fatalf("turns %d, err %s", len(r.Turns), r.Err)
			}
			if got := r.Turns[0].Phase; got != c.want {
				t.Errorf("phase %q, want %q\n%s", got, c.want, r.Turns[0].Feedback)
			}
			if r.Success {
				t.Error("broken program accepted")
			}
			if strings.Contains(r.Turns[0].Feedback, os.TempDir()) {
				t.Error("feedback leaks harness paths")
			}
		})
	}
}
