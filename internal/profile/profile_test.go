package profile

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSharedComponentsAreValidatedAndIndependent(t *testing.T) {
	saved := FS
	t.Cleanup(func() { FS = saved })
	base := "id = \"test\"\nversion = \"1\"\nboards = [\"board\"]\npath = [\"{tc}/bin\"]\nshared_components = [\"tc\"]\n"
	component := "name = \"tc\"\nversion = \"1\"\nlicense = \"BSD-3-Clause\"\n[sources.linux-x64]\nurl = \"https://example.invalid/tc.tar.gz\"\nsha256 = \"" + strings.Repeat("a", 64) + "\"\nformat = \"tar.gz\"\n"
	FS = fstest.MapFS{"profiles/test.toml": {Data: []byte(base)}, "components/tc.toml": {Data: []byte(component)}}
	a, err := Load("test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load("test")
	if err != nil {
		t.Fatal(err)
	}
	a.Components[0].Sources["linux-x64"] = Source{URL: "changed"}
	if b.Components[0].Sources["linux-x64"].URL == "changed" {
		t.Fatal("shared component state leaked between profiles")
	}
	for _, tc := range []struct{ name, profile, component string }{
		{"traversal", strings.Replace(base, `["tc"]`, `["../tc"]`, 1), component},
		{"duplicate", strings.Replace(base, `["tc"]`, `["tc","tc"]`, 1), component},
		{"wrong-name", base, strings.Replace(component, `name = "tc"`, `name = "other"`, 1)},
		{"unknown-key", base, component + "unexpected = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			FS = fstest.MapFS{"profiles/test.toml": {Data: []byte(tc.profile)}, "components/tc.toml": {Data: []byte(tc.component)}}
			if _, err := Load("test"); err == nil {
				t.Fatal("invalid shared component accepted")
			}
		})
	}
}

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
		for _, platform := range []string{"linux-x64", "windows-x64", "macos-arm64", "macos-x64"} {
			for _, c := range p.Components {
				if !c.Supports(platform) {
					continue
				}
				s, _, err := c.Source(platform)
				if err != nil {
					t.Errorf("%s: %v", id, err)
					continue
				}
				if len(s.Digest) != 71 || s.Digest[:7] != "sha256:" || !sha256Re.MatchString(s.Digest[7:]) {
					t.Errorf("%s %s: package digest is not pinned", c.Name, platform)
				}
			}
		}
	}
}

func TestHostOverridesDoNotMutateProfile(t *testing.T) {
	p, err := Load("rp2040")
	if err != nil {
		t.Fatal(err)
	}
	win := p.ForHost("windows-x64")
	if win.Path[0] != "{python}" || p.Path[0] != "{python}/bin" {
		t.Fatal("host PATH override changed Linux")
	}
	if !slices.Contains(win.Configure, "-DPython3_EXECUTABLE={python}/python.exe") || slices.Contains(p.Configure, "-DPython3_EXECUTABLE={python}/python.exe") {
		t.Fatal("host configure flags are not isolated")
	}
	if win.Env["PICO_SDK_PATH"] != "{pico-sdk}" || win.Env["PYTHONNOUSERSITE"] != "1" {
		t.Fatal("host environment did not merge with defaults")
	}
	win.Path[0] = "changed"
	win.Env["PICO_SDK_PATH"] = "changed"
	win.Configure[0] = "changed"
	if p.Path[0] != "{python}/bin" || p.Env["PICO_SDK_PATH"] != "{pico-sdk}" || p.Configure[0] != "-G" {
		t.Fatal("effective host profile aliases shared data")
	}
}

func TestIntelRuntimeIsHostSpecific(t *testing.T) {
	p, err := Load("rp2040")
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"linux-x64", "windows-x64", "macos-arm64", "macos-x64"} {
		found := false
		for _, c := range p.ComponentsFor(host) {
			if c.Name == "zstd" {
				found = true
			}
		}
		if found != (host == "macos-x64") {
			t.Fatalf("unexpected zstd dependency for %s", host)
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
