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

func TestLayoutValidation(t *testing.T) {
	for _, tt := range []struct {
		platform, layout string
		valid            bool
	}{
		{"linux-x64", "", true}, {"windows-x64", LayoutWindows, true},
		{"any", LayoutWindows, false}, {"linux-x64", LayoutWindows, false}, {"windows-x64", "typo", false},
	} {
		p := Profile{ID: "test", Version: "1", Boards: []string{"board"}, Components: []Component{{Name: "sdk", Sources: map[string]Source{tt.platform: {URL: "https://example.invalid/sdk.zip", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Format: "zip", Layout: tt.layout}}}}}
		if err := p.validate(); (err == nil) != tt.valid {
			t.Errorf("layout %q on %s: %v", tt.layout, tt.platform, err)
		}
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
