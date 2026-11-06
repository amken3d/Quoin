package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func targetsOf(src string) []string {
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "// targets:") {
			return strings.Fields(strings.TrimPrefix(l, "// targets:"))
		}
	}
	return []string{"c", "m33-qemu"}
}

func hasTarget(src, t string) bool {
	for _, x := range targetsOf(src) {
		if x == t {
			return true
		}
	}
	return false
}

func needQEMU(t *testing.T) {
	for _, tool := range []string{"qemu-system-arm", "arm-none-eabi-as", "arm-none-eabi-ld", "arm-none-eabi-nm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

// TestQEMURun compiles every testdata/run program with the native Thumb-2
// backend, runs it on QEMU's Cortex-M33, and requires (1) the same output
// as the C backend's `// want:` block and (2) measured stack and data-stack
// high-water marks no larger than the proven bounds.
func TestQEMURun(t *testing.T) {
	needQEMU(t)
	files, _ := filepath.Glob("../../testdata/run/*.qn")
	for _, f := range files {
		src, _ := os.ReadFile(f)
		if !hasTarget(string(src), "m33-qemu") {
			continue
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			r := Compile([]Source{{Name: filepath.Base(f), Text: src}}, Options{Target: "m33-qemu"})
			if r.Diags.HasErrors() {
				t.Fatalf("compile failed:\n%s", r.Diags.Text())
			}
			_, elf, err := r.WriteNative(t.TempDir(), f, true)
			if err != nil || elf == "" {
				t.Fatalf("link: %v", err)
			}
			run, err := RunQEMU(elf, false, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			want := wantBlock(string(src))
			if got := strings.TrimSpace(run.Output); got != want {
				t.Errorf("output:\n%s\nwant:\n%s", got, want)
			}
			if run.StackHW >= 0 {
				bound := r.Report.Entries[0].Call
				if run.StackHW > bound {
					t.Errorf("stack bound violated: measured %dB > proven %dB", run.StackHW, bound)
				}
				if run.DStackHW > r.Report.DataTotal {
					t.Errorf("data-stack bound violated: measured %dB > proven %dB", run.DStackHW, r.Report.DataTotal)
				}
				t.Logf("stack %d/%dB data %d/%dB", run.StackHW, bound, run.DStackHW, r.Report.DataTotal)
			}
		})
	}
}
