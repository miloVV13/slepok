package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRegistration(t *testing.T) {
	s := openStore(t)
	start := make(chan struct{})
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- s.Register(filepath.Join(s.root, fmt.Sprintf("project-%d", i)))
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	projects, err := s.Projects()
	if err != nil || len(projects) != 16 {
		t.Fatalf("lost concurrent registrations: %d projects, %v", len(projects), err)
	}
}

func TestRegistry(t *testing.T) {
	s := openStore(t)
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b, a} {
		if err := s.Register(dir); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := s.Projects()
	if err != nil || len(projects) != 2 {
		t.Fatalf("Projects = %v, %v; want 2 unique entries", projects, err)
	}
	if err := s.Forget(a); err != nil {
		t.Fatal(err)
	}
	if projects, _ := s.Projects(); len(projects) != 1 || projects[0].Path != b {
		t.Fatalf("after Forget: %v", projects)
	}
}

func kinds(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Kind+":"+it.Name)
	}
	slices.Sort(out)
	return out
}

func TestGC(t *testing.T) {
	s := openStore(t)
	data, used := newPackage(t)
	if err := s.Install(used, strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
	// A second, unreferenced package.
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "other"), []byte("other"), 0o644)
	unused, err := s.InstallDir(src, "")
	if err != nil {
		t.Fatal(err)
	}

	downloads := filepath.Join(s.root, "downloads")
	os.MkdirAll(downloads, 0o755)
	os.WriteFile(filepath.Join(downloads, "aaaa"), []byte("kept"), 0o644)
	os.WriteFile(filepath.Join(downloads, "bbbb"), []byte("drop"), 0o644)
	os.WriteFile(filepath.Join(downloads, "cccc.part"), []byte("drop"), 0o644)
	tmp, _ := s.TempDir("pack-")
	os.WriteFile(filepath.Join(s.root, ".projects.json.tmp-abandoned"), []byte("partial registry"), 0o600)
	logs := filepath.Join(s.root, "logs")
	os.MkdirAll(logs, 0o755)
	os.WriteFile(filepath.Join(logs, "pack.log"), []byte("log"), 0o644)

	keep := map[string]bool{used: true}
	keepDownloads := map[string]bool{"aaaa": true}

	// Right after installation nothing is old enough to collect except
	// unreferenced downloads.
	now := time.Now()
	items, err := s.PlanGC(keep, keepDownloads, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(items); !slices.Equal(got, []string{"download:bbbb", "download:cccc.part"}) {
		t.Fatalf("plan now = %v", got)
	}

	later := now.Add(30 * 24 * time.Hour)
	items, err = s.PlanGC(keep, keepDownloads, later)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"download:bbbb", "download:cccc.part", "log:pack.log", "package:" + unused, "tmp:" + filepath.Base(tmp), "tmp:.projects.json.tmp-abandoned"}
	slices.Sort(want)
	if got := kinds(items); !slices.Equal(got, want) {
		t.Fatalf("plan later =\n  %v\nwant\n  %v", got, want)
	}
	if err := s.Delete(items); err != nil {
		t.Fatal(err)
	}
	if s.Has(unused) || !s.Has(used) {
		t.Fatalf("after GC: unused present=%v, used present=%v", s.Has(unused), s.Has(used))
	}
	if _, err := os.Stat(filepath.Join(downloads, "aaaa")); err != nil {
		t.Fatal("referenced download was deleted")
	}

	if err := s.Delete([]Item{{Path: "/etc/passwd"}}); err == nil {
		t.Fatal("Delete accepted a path outside the store")
	}
}
