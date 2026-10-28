package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
)

func newPackage(t *testing.T) ([]byte, string) {
	t.Helper()
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "tool"), []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	digest, _, err := pkgtar.Write(&buf, src)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), digest
}

func openStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeTree(root) })
	return s
}

func TestInstall(t *testing.T) {
	s := openStore(t)
	data, digest := newPackage(t)
	if err := s.Install(digest, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if !s.Has(digest) {
		t.Fatal("package not installed")
	}
	p, _ := s.Path(digest)
	info, err := os.Stat(filepath.Join(p, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("installed file is writable: %v", info.Mode())
	}
	// Installing again is a checked no-op.
	if err := s.Install(digest, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(digest); err != nil {
		t.Fatal(err)
	}
	if s.Has(digest) {
		t.Fatal("package still present after Remove")
	}
}

func TestInstallRejectsWrongDigest(t *testing.T) {
	s := openStore(t)
	data, _ := newPackage(t)
	wrong := "sha256:" + strings.Repeat("0", 64)
	err := s.Install(wrong, bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if s.Has(wrong) {
		t.Fatal("package installed despite mismatch")
	}
	left, _ := os.ReadDir(filepath.Join(s.root, "packages"))
	if len(left) != 0 {
		t.Fatalf("staging not cleaned: %v", left)
	}
}

func TestPathRejectsBadDigest(t *testing.T) {
	s := openStore(t)
	for _, d := range []string{"", "sha256:../../etc", "md5:abc", "sha256:" + strings.Repeat("G", 64)} {
		if _, err := s.Path(d); err == nil {
			t.Errorf("Path(%q) succeeded", d)
		}
	}
}
