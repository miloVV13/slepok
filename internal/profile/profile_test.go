package profile

import (
	"slices"
	"testing"
)

func TestBuiltinProfilesLoad(t *testing.T) {
	ids := IDs()
	if !slices.Contains(ids, "rp2040") {
		t.Fatalf("IDs() = %v, want rp2040", ids)
	}
	for _, id := range ids {
		p, err := Load(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Components {
			if _, _, err := c.Source("linux-x64"); err != nil {
				t.Errorf("%s: %v", id, err)
			}
		}
	}
}

func TestLoadUnknown(t *testing.T) {
	if _, err := Load("z80"); err == nil {
		t.Fatal("Load accepted an unknown profile")
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"arm-gcc": "/s/abc", "board": "pico"}
	got, err := Expand("-DPICO_TOOLCHAIN_PATH={arm-gcc} -DPICO_BOARD={board}", vars)
	if err != nil || got != "-DPICO_TOOLCHAIN_PATH=/s/abc -DPICO_BOARD=pico" {
		t.Fatalf("Expand = %q, %v", got, err)
	}
	if _, err := Expand("{nope}/bin", vars); err == nil {
		t.Fatal("Expand accepted an unknown placeholder")
	}
}
