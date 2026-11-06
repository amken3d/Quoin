package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var kernelRe = regexp.MustCompile(`(?m)^// kernel: (\w+)`)

// TestKernels builds each testdata/kernels program natively, runs it on
// QEMU's Cortex-M33 and checks the claims the compiler makes about it:
// output matches the reference, measured stack <= proven stack, and the
// kernel function's executed instruction count <= its static instruction
// bound. Cycle bounds cannot be checked on QEMU (it is not cycle
// accurate); they need DWT_CYCCNT on an RP2350.
func TestKernels(t *testing.T) {
	needQEMU(t)
	files, _ := filepath.Glob("../../testdata/kernels/*.qn")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, _ := os.ReadFile(f)
			m := kernelRe.FindSubmatch(src)
			if m == nil {
				t.Fatal("missing `// kernel: name` line")
			}
			kname := string(m[1])
			r := Compile([]Source{{Name: filepath.Base(f), Text: src}}, Options{Target: "m33-qemu"})
			if r.Diags.HasErrors() {
				t.Fatalf("compile:\n%s", r.Diags.Text())
			}
			_, elf, err := r.WriteNative(t.TempDir(), f, true)
			if err != nil || elf == "" {
				t.Fatalf("link: %v", err)
			}
			run, err := RunQEMU(elf, false, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := strings.TrimSpace(run.Output), wantBlock(string(src)); got != want {
				t.Fatalf("output %q, want %q", got, want)
			}
			if bound := r.Report.Entries[0].Call; run.StackHW > bound || run.DStackHW > r.Report.DataTotal {
				t.Errorf("stack bound violated: %d/%dB, data %d/%dB", run.StackHW, bound, run.DStackHW, r.Report.DataTotal)
			}
			var kb *int64
			var kcyc int64
			for fn, b := range r.Bounds {
				if fn.Name == kname {
					if b.Unbounded || b.NeverReturns {
						t.Fatalf("kernel %s has no bound: %s", kname, b.Why)
					}
					kb, kcyc = &b.Insns, b.Cycles
				}
			}
			if kb == nil {
				t.Fatalf("no bound for kernel %s", kname)
			}
			n, err := CountFn(elf, "qn_"+kname, 60*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if n > *kb {
				t.Errorf("instruction bound violated: executed %d > static %d", n, *kb)
			}
			t.Logf("%s: insns %d/%d (x%.2f), cycle bound %d, stack %d/%dB", kname, n, *kb, float64(*kb)/float64(n), kcyc,
				run.StackHW, r.Report.Entries[0].Call)
		})
	}
}
