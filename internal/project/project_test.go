package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/profile"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(content), 0o644))
}

var cfg = Config{Profile: "rp2040", Board: "pico", Platforms: []string{"linux-x64"}, BuildDir: "build"}

func TestConfigAndLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	must(t, WriteConfig(dir, cfg))
	lock := Lock{Profile: "rp2040", ProfileVersion: "1", Packages: []LockedPackage{
		{Name: "arm-gcc", Version: "14.2", Platform: "linux-x64", Source: profile.Source{URL: "https://x/y.tar.xz", SHA256: strings.Repeat("a", 64), Format: "tar.xz", Strip: 1, Digest: "sha256:" + strings.Repeat("b", 64)}},
		{Name: "pico-sdk", Version: "2.3.1", Platform: "any", Source: profile.Source{Git: "https://x/sdk.git", Ref: "2.3.1", Commit: strings.Repeat("c", 40)}},
	}}
	must(t, WriteLock(dir, lock))

	sub := filepath.Join(dir, "src", "deep")
	must(t, os.MkdirAll(sub, 0o755))
	p, err := Find(sub)
	must(t, err)
	if p.Dir != dir || p.Config.Board != "pico" || p.Lock == nil || len(p.Lock.Packages) != 2 {
		t.Fatalf("Find = %+v", p)
	}
	if got := p.Lock.PackagesFor("linux-x64"); len(got) != 2 {
		t.Errorf("PackagesFor(linux-x64) = %d packages", len(got))
	}
	if got := p.Lock.PackagesFor("windows-x64"); len(got) != 1 || got[0].Name != "pico-sdk" {
		t.Errorf("PackagesFor(windows-x64) = %+v", got)
	}
}

func TestFindWithoutProject(t *testing.T) {
	if _, err := Find(t.TempDir()); err == nil || !strings.Contains(err.Error(), "slepok init") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	bad := map[string]func(*Config){
		"profile":   func(c *Config) { c.Profile = "z80" },
		"board":     func(c *Config) { c.Board = "uno" },
		"platforms": func(c *Config) { c.Platforms = nil },
		"build_dir": func(c *Config) { c.BuildDir = "../out" },
	}
	for name, mutate := range bad {
		c := cfg
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate accepted invalid config", name)
		}
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	dir := t.TempDir()
	must(t, WriteConfig(dir, cfg))
	f, _ := os.OpenFile(filepath.Join(dir, ConfigName), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("bord = \"pico\"\n")
	f.Close()
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "bord") {
		t.Fatalf("err = %v, want unknown key error", err)
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	must(t, WriteConfig(dir, cfg))
	for _, f := range []string{
		"CMakeLists.txt", "src/main.c", "src/lib/util.c",
		"build/blink.uf2", "src/build/keep.c", // only the top-level build dir is excluded
		".git/config", "old.slepok", "tools/__pycache__/x.pyc", ".cache/clangd/index",
		"out/a.o", "out/keep.o", "docs/notes.md",
		".env", "certs/server.pem",
	} {
		write(t, dir, f, "x")
	}
	write(t, dir, IgnoreName, "out/\n!out/keep.o\n**/*.md\n")

	p, err := Load(dir)
	must(t, err)
	sel, err := p.Files()
	must(t, err)
	want := []string{".env", IgnoreName, "CMakeLists.txt", "certs/server.pem", "slepok.toml", "src/build/keep.c", "src/lib/util.c", "src/main.c"}
	slices.Sort(sel.Files)
	if !slices.Equal(sel.Files, want) {
		t.Errorf("Files =\n  %v\nwant\n  %v", sel.Files, want)
	}
	if !slices.Equal(sel.Sensitive, []string{".env", "certs/server.pem"}) {
		t.Errorf("Sensitive = %v", sel.Sensitive)
	}
}

func TestFilesRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, WriteConfig(dir, cfg))
	must(t, os.Symlink("slepok.toml", filepath.Join(dir, "link")))
	p, err := Load(dir)
	must(t, err)
	if _, err := p.Files(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want symlink error", err)
	}
}
