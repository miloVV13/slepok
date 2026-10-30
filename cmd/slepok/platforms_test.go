package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/snapshot"
	"github.com/miloVV13/slepok/internal/store"
)

func TestSelectedPlatforms(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", []string{manifest.HostPlatform()}},
		{"windows-x64", []string{"windows-x64"}},
		{"linux-x64, windows-x64,linux-x64", []string{"linux-x64", "windows-x64"}},
	} {
		got, err := selectedPlatforms(tc.in)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %v, %v", tc.in, got, err)
		}
	}
	for _, in := range []string{"windows", "macos-arm64", "linux-x64,", " "} {
		if _, err := selectedPlatforms(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
}

func TestPlatformSnapshotSelection(t *testing.T) {
	if manifest.HostPlatform() != "linux-x64" {
		t.Skip("producer fixture uses Linux tools")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "cmake")
	if out, err := exec.Command("go", "build", "-o", fake, "./testdata/fakecmake").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	archive := toolchainArchive(t, fake)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()
	url, commit := sdkRepo(t)
	fakeProfile(t, srv.URL, archive, nil, url, commit)
	// This fixture uses a shared tool package to exercise selection without
	// downloading or pretending to run Windows executables on Linux.
	fs := profile.FS.(fstest.MapFS)
	fs["profiles/fake.toml"].Data = []byte(strings.ReplaceAll(string(fs["profiles/fake.toml"].Data), "sources.linux-x64", "sources.any"))
	home := filepath.Join(dir, "store")
	t.Setenv("SLEPOK_HOME", home)
	t.Cleanup(func() { _ = store.RemoveTree(home) })
	t.Chdir(dir)
	must(t, execute([]string{"init", "--profile", "fake", "--platform", "windows-x64", "proj"}))
	t.Chdir(filepath.Join(dir, "proj"))
	p, err := project.Load(".")
	must(t, err)
	if !reflect.DeepEqual(p.Config.Platforms, []string{"windows-x64", "linux-x64"}) {
		t.Fatal(p.Config.Platforms)
	}
	original, err := os.ReadFile(project.ConfigName)
	must(t, err)
	for _, tc := range []struct {
		name, flag string
		want       []string
	}{
		{"host", "", []string{"linux-x64"}},
		{"windows", "windows-x64", []string{"windows-x64"}},
		{"dual", "linux-x64,windows-x64", []string{"linux-x64", "windows-x64"}},
	} {
		file := filepath.Join(dir, tc.name+".slepok")
		args := []string{"pack", "-o", file}
		if tc.flag != "" {
			args = append(args, "--platform", tc.flag)
		}
		must(t, execute(args))
		m, err := snapshot.Validate(file)
		must(t, err)
		if !reflect.DeepEqual(m.Platforms, tc.want) {
			t.Fatal(m.Platforms)
		}
		st, err := store.Open(filepath.Join(dir, "receiver-"+tc.name))
		must(t, err)
		t.Cleanup(func() { _ = store.RemoveTree(st.Root()) })
		restored := filepath.Join(dir, "restored-"+tc.name)
		_, err = snapshot.Unpack(file, restored, st, tc.want[0])
		must(t, err)
		p, err := project.Load(restored)
		must(t, err)
		if !reflect.DeepEqual(p.Config.Platforms, tc.want) {
			t.Fatalf("restored config = %v", p.Config.Platforms)
		}
	}
	current, err := os.ReadFile(project.ConfigName)
	must(t, err)
	if string(current) != string(original) {
		t.Fatal("pack modified source config")
	}
}
