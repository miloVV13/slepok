package pkgtar

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "bin"), 0o700))
	must(os.MkdirAll(filepath.Join(dir, "lib", "sub"), 0o700))
	must(os.WriteFile(filepath.Join(dir, "bin", "tool"), []byte("#!/bin/sh\necho hi\n"), 0o700))
	must(os.WriteFile(filepath.Join(dir, "lib", "data.txt"), []byte("data"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "lib", "sub", "empty"), nil, 0o640))
	must(os.Link(filepath.Join(dir, "bin", "tool"), filepath.Join(dir, "bin", "tool-1.0")))
	must(os.Symlink("../lib/data.txt", filepath.Join(dir, "bin", "data")))
}

func pack(t *testing.T, dir string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	digest, size, err := Write(&buf, dir)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(buf.Len()) {
		t.Fatalf("size = %d, wrote %d bytes", size, buf.Len())
	}
	return buf.Bytes(), digest
}

func TestRoundTripIsCanonical(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src)
	data1, digest1 := pack(t, src)
	_, digest2 := pack(t, src)
	if digest1 != digest2 {
		t.Fatalf("digest not deterministic: %s vs %s", digest1, digest2)
	}

	dest := filepath.Join(t.TempDir(), "pkg")
	if err := Extract(bytes.NewReader(data1), dest); err != nil {
		t.Fatal(err)
	}
	if _, digest3 := pack(t, dest); digest3 != digest1 {
		t.Fatalf("extracted tree digest %s != original %s", digest3, digest1)
	}

	info, err := os.Stat(filepath.Join(dest, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("tool mode = %v, want 0755", info.Mode().Perm())
	}
	linked, err := os.Stat(filepath.Join(dest, "bin", "tool-1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, linked) {
		t.Error("hardlink not preserved")
	}
	if target, err := os.Readlink(filepath.Join(dest, "bin", "data")); err != nil || target != "../lib/data.txt" {
		t.Errorf("symlink = %q, %v", target, err)
	}
}

func TestDigestChangesWithContent(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src)
	_, before := pack(t, src)
	if err := os.WriteFile(filepath.Join(src, "lib", "data.txt"), []byte("Data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, after := pack(t, src); after == before {
		t.Fatal("digest did not change after content change")
	}
}

func TestWriteRejectsEscapingSymlink(t *testing.T) {
	src := t.TempDir()
	if err := os.Symlink("../../etc/passwd", filepath.Join(src, "evil")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Write(&bytes.Buffer{}, src); err == nil {
		t.Fatal("Write accepted a symlink escaping the package")
	}
}

type entry struct {
	name, link string
	typ        byte
	body       string
}

func craft(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: 0o644, Size: int64(len(e.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMaliciousArchives(t *testing.T) {
	dir := entry{name: "d", typ: tar.TypeDir}
	tests := map[string][]entry{
		"dotdot":             {{name: "../evil", typ: tar.TypeReg, body: "x"}},
		"absolute":           {{name: "/tmp/evil", typ: tar.TypeReg, body: "x"}},
		"nested dotdot":      {dir, {name: "d/../../evil", typ: tar.TypeReg, body: "x"}},
		"duplicate":          {{name: "f", typ: tar.TypeReg}, {name: "f", typ: tar.TypeReg}},
		"missing parent":     {{name: "d/f", typ: tar.TypeReg}},
		"symlink escape":     {{name: "l", link: "../outside", typ: tar.TypeSymlink}},
		"symlink absolute":   {{name: "l", link: "/etc", typ: tar.TypeSymlink}},
		"write through link": {dir, {name: "l", link: "d", typ: tar.TypeSymlink}, {name: "l/f", typ: tar.TypeReg, body: "x"}},
		"hardlink missing":   {{name: "h", link: "nope", typ: tar.TypeLink}},
		"hardlink to dir":    {dir, {name: "h", link: "d", typ: tar.TypeLink}},
		"hardlink outside":   {{name: "h", link: "../../etc/passwd", typ: tar.TypeLink}},
		"char device":        {{name: "c", typ: tar.TypeChar}},
		"fifo":               {{name: "p", typ: tar.TypeFifo}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			data := craft(t, entries...)
			if err := Verify(bytes.NewReader(data)); err == nil {
				t.Error("Verify accepted malicious archive")
			}
			parent := t.TempDir()
			dest := filepath.Join(parent, "pkg")
			if err := Extract(bytes.NewReader(data), dest); err == nil {
				t.Error("Extract accepted malicious archive")
			}
			got, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range got {
				if e.Name() != "pkg" {
					t.Errorf("Extract wrote %q outside destination", e.Name())
				}
			}
		})
	}
}

// TestRealTrees round-trips real toolchain directories, e.g.
// SLEPOK_REAL_TREES=prototype/work/env/arm-gcc:prototype/work/env/python
func TestRealTrees(t *testing.T) {
	trees := os.Getenv("SLEPOK_REAL_TREES")
	if trees == "" {
		t.Skip("SLEPOK_REAL_TREES not set")
	}
	for _, src := range filepath.SplitList(trees) {
		t.Run(filepath.Base(src), func(t *testing.T) {
			data, digest := pack(t, src)
			if err := Verify(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "pkg")
			if err := Extract(bytes.NewReader(data), dest); err != nil {
				t.Fatal(err)
			}
			if _, again := pack(t, dest); again != digest {
				t.Fatalf("digest after extract %s != %s", again, digest)
			}
			t.Logf("%s: %d MiB, %s", src, len(data)>>20, digest)
		})
	}
}

func TestExtractRequiresNewDestination(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src)
	data, _ := pack(t, src)
	err := Extract(bytes.NewReader(data), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("Extract into existing dir: err = %v, want 'exists'", err)
	}
}
