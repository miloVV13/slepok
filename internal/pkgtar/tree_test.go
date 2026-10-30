package pkgtar

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPortableTreeMatchesUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix reference tree")
	}
	dir := t.TempDir()
	for _, d := range []string{"a", "empty", "a/sub"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "z"), []byte("tool"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "z"), filepath.Join(dir, "a", "tool")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../z", filepath.Join(dir, "a", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tr := NewTree(t.TempDir())
	for _, d := range []string{"a", "empty", "a/sub"} {
		if err := tr.Dir(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.File(ctx, "z", 0755, strings.NewReader("tool")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Link("a/tool", "z"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Symlink("a/link", "../z"); err != nil {
		t.Fatal(err)
	}
	if err := tr.File(ctx, "a.txt", 0644, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	var native, portable bytes.Buffer
	want, _, err := Write(&native, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := tr.Write(ctx, &portable)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !bytes.Equal(native.Bytes(), portable.Bytes()) {
		t.Fatalf("canonical output differs: %s != %s", got, want)
	}
}

func TestPortableTreeRejectsConflictsAndEscapes(t *testing.T) {
	tr := NewTree(t.TempDir())
	if err := tr.Symlink("alias", "dir"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside", "alias/file"} {
		if err := tr.File(t.Context(), name, 0644, strings.NewReader("x")); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if err := tr.Link("bad", "missing"); err == nil {
		t.Fatal("accepted broken hardlink")
	}
	if err := tr.Symlink("bad", "../outside"); err == nil {
		t.Fatal("accepted escaping link")
	}
	if err := tr.File(t.Context(), "file", 0644, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := tr.File(t.Context(), "file", 0644, strings.NewReader("y")); err == nil {
		t.Fatal("accepted duplicate file")
	}
}
