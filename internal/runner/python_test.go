package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

func TestProjectPythonEnvironment(t *testing.T) {
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { store.RemoveTree(st.Root()) })
	work := t.TempDir()
	must(t, os.WriteFile(filepath.Join(work, "demo.py"), []byte("value=42\n"), 0o644))
	digest, err := st.InstallDir(work, "")
	must(t, err)
	root, err := st.Path(digest)
	must(t, err)
	prof := &profile.Profile{Env: map[string]string{"PYTHONPATH": "profile-libraries"}}
	lock := &project.Lock{PythonRequirements: []string{"demo==1"}, Packages: []project.LockedPackage{{Name: "python", Platform: "linux-x64", Source: profile.Source{Digest: digest}}, {Name: "project-python", Platform: "linux-x64", Source: profile.Source{Digest: digest}}}}
	e, err := Resolve(prof, lock, st, "linux-x64", "pico")
	must(t, err)
	if e.Vars["PYTHONPATH"] != root+string(os.PathListSeparator)+"profile-libraries" || e.Vars["PYTHONNOUSERSITE"] != "1" || e.Vars["PYTHONDONTWRITEBYTECODE"] != "1" {
		t.Fatal(e.Vars)
	}
	if _, err := Resolve(prof, lock, st, "windows-x64", "pico"); err == nil {
		t.Fatal("missing foreign Python bundle accepted")
	}
}
