package manifest

import (
	"errors"
	"strings"
	"testing"
)

const goodDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const otherDigest = "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func valid() *Manifest {
	return &Manifest{
		FormatVersion: FormatVersion,
		Platforms:     []string{"linux-x64"},
		Packages:      []Package{{Name: "arm-gcc", Version: "14.2.rel1", Platform: "linux-x64", Digest: goodDigest, Size: 10}},
		Files:         []File{{Path: "project/CMakeLists.txt", Size: 3, Digest: otherDigest}},
	}
}

func TestRoundTrip(t *testing.T) {
	data, err := valid().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Packages[0].Entry(); got != "packages/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.tar" {
		t.Errorf("Entry() = %q", got)
	}
}

func TestParseVersion(t *testing.T) {
	_, err := Parse([]byte(`{"formatVersion": 7, "somethingNew": true}`))
	var tooNew *TooNewError
	if !errors.As(err, &tooNew) || tooNew.Version != 7 {
		t.Fatalf("err = %v, want TooNewError{7}", err)
	}
	for _, data := range []string{`{}`, `{"formatVersion": 0}`, `not json`} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("Parse(%s) succeeded", data)
		}
	}
}

func TestParseIgnoresUnknownFields(t *testing.T) {
	data, _ := valid().Marshal()
	data = []byte(strings.Replace(string(data), `"formatVersion": 1,`, `"formatVersion": 1, "futureField": 42,`, 1))
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]func(m *Manifest){
		"no platforms":      func(m *Manifest) { m.Platforms = nil },
		"bad platform":      func(m *Manifest) { m.Platforms = []string{"Linux x64"} },
		"package platform":  func(m *Manifest) { m.Packages[0].Platform = "windows-x64" },
		"package digest":    func(m *Manifest) { m.Packages[0].Digest = "sha256:XYZ" },
		"duplicate package": func(m *Manifest) { m.Packages = append(m.Packages, m.Packages[0]) },
		"file outside":      func(m *Manifest) { m.Files[0].Path = "etc/passwd" },
		"file traversal":    func(m *Manifest) { m.Files[0].Path = "project/../manifest.json" },
		"file digest":       func(m *Manifest) { m.Files[0].Digest = goodDigest[:20] },
		"duplicate file":    func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) },
		"file is also a dir": func(m *Manifest) {
			m.Files = append(m.Files, File{Path: "project/CMakeLists.txt/x", Digest: goodDigest})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			m := valid()
			mutate(m)
			if err := m.Validate(); err == nil {
				t.Error("Validate accepted invalid manifest")
			}
		})
	}
}
