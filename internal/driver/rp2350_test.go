package driver

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestRP2350MMIO builds the RP2350 blink example as a real rp2350 image and
// runs its main() in Unicorn with mocked peripherals (tools/mmio_rp2350.py),
// asserting the exact sequence of peripheral writes. QEMU has no RP2350.
func TestRP2350MMIO(t *testing.T) {
	needQEMU(t) // binutils
	if err := exec.Command("python3", "-c", "import unicorn, elftools").Run(); err != nil {
		t.Skip("python3 with unicorn and pyelftools not available")
	}
	srcs, err := Load([]string{"../../examples/rp2350_blink.qn"})
	if err != nil {
		t.Fatal(err)
	}
	r := Compile(srcs, Options{Target: "rp2350"})
	if r.Diags.HasErrors() {
		t.Fatalf("compile:\n%s", r.Diags.Text())
	}
	files, elf, err := r.WriteNative(t.TempDir(), "rp2350_blink.qn", true)
	if err != nil || elf == "" {
		t.Fatalf("link: %v", err)
	}
	hasUF2 := false
	for _, f := range files {
		if len(f) > 4 && f[len(f)-4:] == ".uf2" {
			hasUF2 = true
		}
	}
	if !hasUF2 {
		t.Error("no UF2 produced")
	}
	out, err := exec.Command("python3", "../../tools/mmio_rp2350.py", elf, "9").Output()
	if err != nil {
		t.Fatalf("unicorn: %v", err)
	}
	var res struct {
		Events []struct {
			Op    string `json:"op"`
			Addr  uint64 `json:"addr"`
			Value uint64 `json:"value"`
		} `json:"events"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	type w struct{ addr, value uint64 }
	var got []w
	for _, e := range res.Events {
		if e.Op == "w" {
			got = append(got, w{e.Addr, e.Value})
		}
	}
	want := []w{
		{0x40020000, 0x1fffffbf}, // RESETS.RESET.IO_BANK0 = false
		{0x40020000, 0x1ffffdbf}, // RESETS.RESET.PADS_BANK0 = false
		{0x40038068, 0x96},       // PADS GPIO25.ISO = false
		{0x40038068, 0x16},       // PADS GPIO25.OD = false
		{0x400280cc, 0x5},        // GPIO25_CTRL.FUNCSEL = .Sio
		{0xd0000038, 1 << 25},    // SIO.GPIO_OE_SET
		{0xd0000028, 1 << 25},    // toggle
		{0xd0000028, 1 << 25},
		{0xd0000028, 1 << 25},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d writes, want %d: %x", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("write %d: got %#x=%#x, want %#x=%#x", i, got[i].addr, got[i].value, want[i].addr, want[i].value)
		}
	}
}
