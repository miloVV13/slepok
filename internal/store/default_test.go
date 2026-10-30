package store

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultRootWithoutOverride(t *testing.T) {
	t.Setenv("SLEPOK_HOME", "")
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", dir)
	} else {
		t.Setenv("XDG_DATA_HOME", dir)
	}
	got, err := DefaultRoot()
	if err != nil || got != filepath.Join(dir, "slepok") {
		t.Fatalf("root = %q, %v", got, err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", "")
		t.Setenv("USERPROFILE", dir)
		got, err = DefaultRoot()
		if err != nil || got != filepath.Join(dir, "AppData", "Local", "slepok") {
			t.Fatalf("fallback = %q, %v", got, err)
		}
	}
}

func TestRelativeStoreRoot(t *testing.T) {
	t.Chdir(t.TempDir())
	s, err := Open("relative-store")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(s.Root()) {
		t.Fatal("store root is relative")
	}
	t.Chdir(t.TempDir())
	if _, err := s.TempDir("test-"); err != nil {
		t.Fatal(err)
	}
}
