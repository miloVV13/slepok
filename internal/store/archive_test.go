package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveInstallPromoteAndGC(t *testing.T) {
	s := openStore(t)
	data, digest := newPackage(t)
	if err := s.InstallArchiveContext(t.Context(), digest, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if !s.Has(digest) || s.HasTree(digest) {
		t.Fatal("archive treated as runnable tree")
	}
	file, _ := s.ArchivePath(digest)
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("archive bytes changed", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanGC(map[string]bool{digest: true}, nil, time.Now())
	if err != nil || len(plan) != 0 {
		t.Fatal("collected referenced archive", plan, err)
	}
	plan, err = s.PlanGC(nil, nil, time.Now())
	if err != nil || len(plan) != 1 || plan[0].Kind != "package" {
		t.Fatal(plan, err)
	}
	if err := s.InstallContext(t.Context(), digest, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if !s.HasTree(digest) {
		t.Fatal("could not promote archive to runnable tree")
	}
	pkgs, err := s.Packages()
	if err != nil || len(pkgs) != 1 {
		t.Fatal("duplicate package", pkgs, err)
	}
	if err := s.Remove(digest); err != nil {
		t.Fatal(err)
	}
	if s.Has(digest) {
		t.Fatal("archive survived Remove")
	}
}

func TestArchiveRejectsCorruptionAndCancellation(t *testing.T) {
	s := openStore(t)
	data, digest := newPackage(t)
	wrong := "sha256:" + strings.Repeat("0", 64)
	if err := s.InstallArchiveContext(t.Context(), wrong, bytes.NewReader(data)); err == nil || s.Has(wrong) {
		t.Fatal("published corrupt package")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.InstallArchiveContext(ctx, digest, bytes.NewReader(data)); err == nil || s.Has(digest) {
		t.Fatal("published cancelled package")
	}
	if err := s.InstallArchiveContext(t.Context(), digest, bytes.NewReader(data[:600])); err == nil || s.Has(digest) {
		t.Fatal("published truncated package")
	}
	left, err := os.ReadDir(filepath.Join(s.Root(), "archives"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatal("archive staging leaked", left)
	}
}
