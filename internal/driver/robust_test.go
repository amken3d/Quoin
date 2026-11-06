package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPanics compiles every prefix and every one-line deletion of each
// test and example file. Broken input must produce diagnostics, never a
// crash: an agent's half-written edit is the common case.
func TestNoPanics(t *testing.T) {
	var files []string
	for _, g := range []string{"../../testdata/*/*.qn", "../../examples/*.qn"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		try := func(variant string, text string) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s (%s): panic: %v\n%s", f, variant, r, text)
				}
			}()
			Compile([]Source{{Name: "x.qn", Text: []byte(text)}}, Options{Emit: true})
			Compile([]Source{{Name: "x.qn", Text: []byte(text)}}, Options{Target: "rp2350"})
		}
		for i := range lines {
			try("prefix", strings.Join(lines[:i], "\n"))
			try("delete", strings.Join(append(append([]string{}, lines[:i]...), lines[i+1:]...), "\n"))
		}
		for i := 0; i < len(src); i += 7 {
			try("cut", string(src[:i]))
		}
	}
}
