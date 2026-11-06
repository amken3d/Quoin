package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var wantRe = regexp.MustCompile(`//!((?:\s+[EW]\d{3})+)\s*$`)
var budgetRe = regexp.MustCompile(`(?m)^// budget: (\d+)`)
var targetRe = regexp.MustCompile(`(?m)^// target: ([\w-]+)`)

// TestDiagnostics compiles every testdata/errors/*.qn and requires the set
// of (line, code) error pairs to equal the `//! CODE` annotations.
func TestDiagnostics(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/errors/*.qn")
	if len(files) == 0 {
		t.Fatal("no testdata/errors files")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{}
			for i, line := range strings.Split(string(src), "\n") {
				if m := wantRe.FindStringSubmatch(line); m != nil {
					for _, code := range strings.Fields(m[1]) {
						want[fmt.Sprintf("%d %s", i+1, code)] = true
					}
				}
			}
			opt := Options{}
			if m := targetRe.FindSubmatch(src); m != nil {
				opt.Target = string(m[1])
			}
			if m := budgetRe.FindSubmatch(src); m != nil {
				opt.Budget, _ = strconv.ParseInt(string(m[1]), 10, 64)
			}
			r := Compile([]Source{{Name: filepath.Base(f), Text: src}}, opt)
			got := map[string]bool{}
			for _, d := range r.Diags.Items {
				got[fmt.Sprintf("%d %s", d.Pos.Line, d.Code)] = true
			}
			var missing, extra []string
			for k := range want {
				if !got[k] {
					missing = append(missing, k)
				}
			}
			for k := range got {
				if !want[k] {
					extra = append(extra, k)
				}
			}
			sort.Strings(missing)
			sort.Strings(extra)
			if len(missing) > 0 || len(extra) > 0 {
				t.Errorf("missing %v, unexpected %v\n%s", missing, extra, r.Diags.Text())
			}
			for _, d := range r.Diags.Items {
				if d.Kind == "Unknown" {
					t.Errorf("code %s has no catalog entry", d.Code)
				}
			}
		})
	}
}

// TestRun compiles each testdata/run/*.qn to C, builds it with the host C
// compiler and the harness, runs it, and compares output with the
// trailing `// want:` block.
func TestRun(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no host C compiler")
	}
	files, _ := filepath.Glob("../../testdata/run/*.qn")
	harness, _ := filepath.Abs("../../testdata/run/harness.c")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if !hasTarget(string(src), "c") {
				t.Skip("not a C-target test")
			}
			want := wantBlock(string(src))
			r := Compile([]Source{{Name: filepath.Base(f), Text: src}}, Options{Emit: true, Lines: true})
			if r.Diags.HasErrors() || r.C == "" {
				t.Fatalf("compile failed:\n%s", r.Diags.Text())
			}
			dir := t.TempDir()
			if _, err := r.WriteOutputs(dir, f); err != nil {
				t.Fatal(err)
			}
			stem := strings.TrimSuffix(filepath.Base(f), ".qn")
			bin := filepath.Join(dir, stem)
			for _, opt := range []string{"-O0", "-O2"} {
				cmd := exec.Command(cc, "-std=c99", "-Wall", "-Wextra", "-Werror", "-pedantic", opt,
					filepath.Join(dir, stem+".c"), harness, "-o", bin)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("cc %s: %v\n%s", opt, err, out)
				}
				out, _ := exec.Command(bin).Output()
				if got := strings.TrimSpace(string(out)); got != want {
					t.Errorf("%s output:\n%s\nwant:\n%s", opt, got, want)
				}
			}
		})
	}
}

func wantBlock(src string) string {
	i := strings.Index(src, "// want:")
	if i < 0 {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(src[i:], "\n")[1:] {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "//") {
			break
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(l, "//")))
	}
	return strings.Join(lines, "\n")
}

// TestExamplesCrossCompile builds every example for Cortex-M0+ (RP2040)
// Cortex-M4 (STM32F4) and Cortex-M33 (RP2350) with warnings as errors, and checks that the
// exact stack bound from -fstack-usage is computed.
func TestExamplesCrossCompile(t *testing.T) {
	gcc, err := exec.LookPath("arm-none-eabi-gcc")
	if err != nil {
		t.Skip("arm-none-eabi-gcc not installed")
	}
	files, _ := filepath.Glob("../../examples/*.qn")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			srcs, err := Load([]string{f})
			if err != nil {
				t.Fatal(err)
			}
			r := Compile(srcs, Options{Emit: true, Lines: true})
			if r.Diags.HasErrors() {
				t.Fatalf("compile failed:\n%s", r.Diags.Text())
			}
			dir := t.TempDir()
			if _, err := r.WriteOutputs(dir, f); err != nil {
				t.Fatal(err)
			}
			stem := strings.TrimSuffix(filepath.Base(f), ".qn")
			for _, cpu := range []string{"cortex-m0plus", "cortex-m4", "cortex-m33"} {
				obj := filepath.Join(dir, stem+"-"+cpu+".o")
				cmd := exec.Command(gcc, "-std=c99", "-Wall", "-Wextra", "-Werror", "-pedantic", "-O2",
					"-mcpu="+cpu, "-mthumb", "-fstack-usage", "-c", filepath.Join(dir, stem+".c"), "-o", obj)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", cpu, err, out)
				}
				su := strings.TrimSuffix(obj, ".o") + ".su"
				m, err := readSUForTest(su)
				if err != nil {
					t.Fatal(err)
				}
				exact := Compile(srcs, Options{SU: m})
				if exact.Diags.HasErrors() || exact.Report == nil || !exact.Report.Valid {
					t.Fatalf("%s: exact stack analysis failed:\n%s", cpu, exact.Diags.Text())
				}
				t.Logf("%s: %s", cpu, exact.Report.Line())
			}
		})
	}
}
