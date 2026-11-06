package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/process"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

// Opt-in: needs the pinned native runtime already installed and PyPI access.
func TestRealPythonWheelResolution(t *testing.T) {
	if os.Getenv("SLEPOK_PYTHON_WHEELS_TEST") != "1" {
		t.Skip("set SLEPOK_PYTHON_WHEELS_TEST=1 for real wheel resolution")
	}
	prof, err := profile.Load("rp2040")
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	python := ""
	for _, c := range prof.Components {
		if c.Name == "python" {
			src, _, err := c.Source(manifest.HostPlatform())
			if err != nil {
				t.Fatal(err)
			}
			python, err = st.Path(src.Digest)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if runtime.GOOS == "windows" {
		python = filepath.Join(python, "python.exe")
	} else {
		python = filepath.Join(python, "bin", "python3")
	}
	for _, target := range []string{"linux-x64", "windows-x64", "macos-arm64", "macos-x64"} {
		t.Run(target, func(t *testing.T) {
			work := t.TempDir()
			constraints := filepath.Join(work, "constraints.txt")
			if err := os.WriteFile(constraints, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			report := filepath.Join(work, "report.json")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := process.Command(ctx, python, "-I", "-c", resolvePython, target, report, constraints, "Jinja2==3.1.6", "pytest==8.3.5")
			cmd.Env = append(os.Environ(), "PIP_CONFIG_FILE="+os.DevNull)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			src, err := pythonSource(data)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, a := range src.Python {
				seen[strings.ToLower(a.Name)] = true
			}
			if !seen["jinja2"] || !seen["markupsafe"] || !seen["pytest"] {
				t.Fatal("incomplete graph", seen)
			}
			if seen["colorama"] != (target == "windows-x64") {
				t.Fatal("incorrect Windows dependency markers", seen)
			}
			if seen["exceptiongroup"] {
				t.Fatal("Python <3.11 dependency selected for 3.12")
			}
		})
	}
}
