package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
)

func TestPythonReportPinsOnlyChecksummedWheels(t *testing.T) {
	for _, tc := range []struct {
		url, hash string
		valid     bool
	}{
		{"https://files.pythonhosted.org/demo-1-py3-none-any.whl", strings.Repeat("a", 64), true},
		{"https://example.org/demo.tar.gz", strings.Repeat("a", 64), false},
		{"http://example.org/demo.whl", strings.Repeat("a", 64), false},
		{"https://example.org/demo.whl", "", false},
		{"https://example.org/demo.whl", strings.Repeat("z", 64), false},
	} {
		report := pythonReport{Version: "1", Install: make([]pythonReportItem, 1)}
		report.Install[0].Download.URL = tc.url
		report.Install[0].Download.Archive.Hashes = map[string]string{"sha256": tc.hash}
		report.Install[0].Metadata.Name, report.Install[0].Metadata.Version = "demo", "1"
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		src, err := pythonSource(data)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.url, err)
		}
		if tc.valid && (src.Python[0].Name != "demo" || src.Python[0].Version != "1") {
			t.Fatal("missing transitive version")
		}
	}
}

func TestProjectPythonAddsRuntimeOnlyWhenRequested(t *testing.T) {
	prof, err := profile.Load("stm32f103c8")
	if err != nil {
		t.Fatal(err)
	}
	cfg := project.Config{Profile: prof.ID, Board: "blue-pill", Platforms: []string{manifest.HostPlatform()}, BuildDir: "build"}
	for _, enabled := range []bool{false, true} {
		if enabled {
			cfg.Python.Requirements = []string{"Jinja2==3.1.6"}
		}
		lock, err := lockFor(t.TempDir(), cfg, prof)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, p := range lock.Packages {
			if p.Name == "python" {
				found = true
			}
		}
		if found != enabled {
			t.Fatalf("Python runtime present=%v, requested=%v", found, enabled)
		}
	}
}
