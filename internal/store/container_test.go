package store

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloVV13/slepok/internal/pkgtar"
)

func TestContainerPublishesSealedTreeAndGC(t *testing.T) {
	s := openStore(t)
	data, digest := newPackage(t)
	if err := s.installContext(t.Context(), digest, bytes.NewReader(data), true); err != nil {
		t.Fatal(err)
	}
	p, err := s.Path(digest)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "tree" || !s.HasTree(digest) {
		t.Fatalf("invalid container path %s", p)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm()&0222 != 0 {
		t.Fatal("payload root is writable", err)
	}
	exported, _, err := pkgtar.Write(io.Discard, p)
	if err != nil || exported != digest {
		t.Fatal("container changed package digest", exported, err)
	}
	outer := filepath.Dir(p)
	info, err = os.Stat(outer)
	if err != nil || info.Mode().Perm()&0200 == 0 {
		t.Fatal("outer container cannot be renamed", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(outer, old, old); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanGC(map[string]bool{digest: true}, nil, time.Now())
	if err != nil || len(plan) != 0 {
		t.Fatal("referenced container collected", plan, err)
	}
	plan, err = s.PlanGC(nil, nil, time.Now())
	if err != nil || len(plan) != 1 || plan[0].Path != outer {
		t.Fatal("container missing from GC", plan, err)
	}
	pkgs, err := s.Packages()
	if err != nil || len(pkgs) != 1 || pkgs[0] != digest {
		t.Fatal(pkgs, err)
	}
	if err := s.Delete(plan); err != nil {
		t.Fatal(err)
	}
	if s.Has(digest) {
		t.Fatal("container survived GC")
	}
	// Legacy trees remain readable and need no migration.
	if err := s.installContext(t.Context(), digest, bytes.NewReader(data), false); err != nil {
		t.Fatal(err)
	}
	p, err = s.Path(digest)
	if err != nil || filepath.Base(p) == "tree" || !s.HasTree(digest) {
		t.Fatal("legacy tree lost", p, err)
	}
}
