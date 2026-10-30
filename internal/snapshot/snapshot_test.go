package snapshot

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/store"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func makeProject(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, "tools"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte("project(x)\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "src", "main.c"), []byte("int main(void){}\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "tools", "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	return dir, []string{"CMakeLists.txt", "src/main.c", "tools/run.sh"}
}

func makePackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "bin", "gcc"), []byte("gcc"), 0o755))
	must(t, os.Link(filepath.Join(dir, "bin", "gcc"), filepath.Join(dir, "bin", "cc")))
	must(t, os.Symlink("gcc", filepath.Join(dir, "bin", "gcc-14")))
	return dir
}

// makeWritable lets t.TempDir clean up read-only store trees.
func makeWritable(t *testing.T, dir string) {
	t.Cleanup(func() {
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type()&fs.ModeSymlink == 0 {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	makeWritable(t, root)
	st, err := store.Open(root)
	must(t, err)
	return st
}

func options(t *testing.T) Options {
	dir, files := makeProject(t)
	return Options{
		CreatedBy:    "slepok-test",
		Profile:      manifest.Profile{ID: "rp2040", Version: "1"},
		Platforms:    []string{"linux-x64"},
		ProjectDir:   dir,
		ProjectFiles: files,
		Packages:     []PackageSource{{Name: "toolchain", Version: "1", Platform: "linux-x64", Dir: makePackage(t)}},
		Report:       []byte(`{"ok":true}`),
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	must(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPackValidateUnpack(t *testing.T) {
	opt := options(t)
	dst := filepath.Join(t.TempDir(), "abc.slepok")
	packed, err := Pack(dst, opt)
	must(t, err)
	if got := listDir(t, filepath.Dir(dst)); len(got) != 1 {
		t.Fatalf("output dir contains %v, want only abc.slepok", got)
	}
	if info, err := os.Stat(dst); err != nil || info.Mode().Perm()&0o044 != 0o044 {
		t.Fatalf("snapshot mode %v, want readable by others", info.Mode())
	}

	m, err := Validate(dst)
	must(t, err)
	if len(m.Files) != 4 || len(m.Packages) != 1 || m.Packages[0].Digest != packed.Packages[0].Digest {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if head, err := ReadManifest(dst); err != nil || head.CreatedBy != "slepok-test" {
		t.Fatalf("ReadManifest = %+v, %v", head, err)
	}

	st := openStore(t)
	dest := filepath.Join(t.TempDir(), "restored")
	_, err = Unpack(dst, dest, st, "linux-x64")
	must(t, err)

	for _, rel := range opt.ProjectFiles {
		want, _ := os.ReadFile(filepath.Join(opt.ProjectDir, rel))
		got, err := os.ReadFile(filepath.Join(dest, rel))
		must(t, err)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: content differs", rel)
		}
	}
	if info, err := os.Stat(filepath.Join(dest, "tools", "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh lost its executable bit: %v, %v", info, err)
	}
	if got := listDir(t, filepath.Dir(dest)); len(got) != 1 {
		t.Errorf("staging left behind: %v", got)
	}

	digest := packed.Packages[0].Digest
	if !st.Has(digest) {
		t.Fatal("package not installed")
	}
	// The installed tree reproduces the packed digest.
	p, _ := st.Path(digest)
	if again, _, err := pkgtar.Write(io.Discard, p); err != nil || again != digest {
		t.Fatalf("store digest %s, %v; want %s", again, err, digest)
	}
}

func TestExtras(t *testing.T) {
	opt := options(t)
	src := filepath.Join(t.TempDir(), "gcc-src.tar.xz")
	must(t, os.WriteFile(src, []byte("source code"), 0o644))
	opt.Extras = []Extra{
		{Name: "reports/NOTICE.txt", Data: []byte("notice")},
		{Name: "sources/gcc-src.tar.xz", Path: src},
	}
	opt.Packages[0].SourceCode = "https://example.com/src.tar.xz"
	opt.Packages[0].Notice = "be careful"
	dst := filepath.Join(t.TempDir(), "abc.slepok")
	_, err := Pack(dst, opt)
	must(t, err)

	m, err := Validate(dst)
	must(t, err)
	var names []string
	for _, f := range m.Files {
		names = append(names, f.Path)
	}
	for _, want := range []string{"reports/NOTICE.txt", "sources/gcc-src.tar.xz"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("manifest lacks %s: %v", want, names)
		}
	}
	if p := m.Packages[0]; p.SourceCode == "" || p.Notice != "be careful" {
		t.Errorf("package notice fields lost: %+v", p)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	_, err = Unpack(dst, dest, openStore(t), "linux-x64")
	must(t, err)
	for _, gone := range []string{"NOTICE.txt", "gcc-src.tar.xz", "reports", "sources"} {
		if _, err := os.Stat(filepath.Join(dest, gone)); err == nil {
			t.Errorf("%s was restored into the project", gone)
		}
	}

	opt.Extras = []Extra{{Name: "etc/passwd", Data: []byte("x")}}
	failedOutput := t.TempDir()
	if _, err := Pack(filepath.Join(failedOutput, "bad.slepok"), opt); err == nil {
		t.Fatal("Pack accepted an extra outside reports/ and sources/")
	}
	if got := listDir(t, failedOutput); len(got) != 0 {
		t.Fatalf("failed validation left output or temporary files: %v", got)
	}
}

func TestPackRefusesExisting(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "abc.slepok")
	must(t, os.WriteFile(dst, []byte("keep"), 0o644))
	if _, err := Pack(dst, options(t)); err == nil {
		t.Fatal("Pack overwrote an existing file")
	}
	if data, _ := os.ReadFile(dst); string(data) != "keep" {
		t.Fatal("existing file was modified")
	}
}

func TestPackFailureLeavesNothing(t *testing.T) {
	opt := options(t)
	opt.ProjectFiles = append(opt.ProjectFiles, "missing.c")
	out := t.TempDir()
	if _, err := Pack(filepath.Join(out, "abc.slepok"), opt); err == nil {
		t.Fatal("Pack succeeded with a missing file")
	}
	if got := listDir(t, out); len(got) != 0 {
		t.Fatalf("Pack left %v behind", got)
	}
}

func TestPackRejectsSymlinkInProject(t *testing.T) {
	opt := options(t)
	must(t, os.Symlink("CMakeLists.txt", filepath.Join(opt.ProjectDir, "link.txt")))
	opt.ProjectFiles = append(opt.ProjectFiles, "link.txt")
	if _, err := Pack(filepath.Join(t.TempDir(), "abc.slepok"), opt); err == nil || !strings.Contains(err.Error(), "regular files") {
		t.Fatalf("err = %v, want regular-files error", err)
	}
}

func TestValidateDetectsCorruption(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "abc.slepok")
	_, err := Pack(dst, options(t))
	must(t, err)
	data, err := os.ReadFile(dst)
	must(t, err)
	for _, off := range []int{len(data) / 3, len(data) / 2, len(data) - 8} {
		corrupt := bytes.Clone(data)
		corrupt[off] ^= 0xff
		bad := filepath.Join(t.TempDir(), "bad.slepok")
		must(t, os.WriteFile(bad, corrupt, 0o644))
		if _, err := Validate(bad); err == nil {
			t.Errorf("Validate accepted archive corrupted at offset %d of %d", off, len(data))
		}
	}
}

type rawEntry struct {
	name string
	data []byte
}

func digestOfBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return pkgtar.Digest(sum[:])
}

func craftSnapshot(t *testing.T, entries ...rawEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "crafted.slepok")
	f, err := os.Create(p)
	must(t, err)
	defer f.Close()
	zw, err := zstd.NewWriter(f)
	must(t, err)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		must(t, tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.data))}))
		_, err := tw.Write(e.data)
		must(t, err)
	}
	must(t, tw.Close())
	must(t, zw.Close())
	return p
}

func manifestFor(t *testing.T, mutate func(*manifest.Manifest), files ...rawEntry) rawEntry {
	t.Helper()
	m := &manifest.Manifest{FormatVersion: 1, Platforms: []string{"linux-x64"}}
	for _, f := range files {
		m.Files = append(m.Files, manifest.File{Path: f.name, Size: int64(len(f.data)), Digest: digestOfBytes(f.data)})
	}
	if mutate != nil {
		mutate(m)
	}
	data, err := m.Marshal()
	must(t, err)
	return rawEntry{manifest.EntryName, data}
}

func maliciousPackage(t *testing.T) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	must(t, tw.WriteHeader(&tar.Header{Name: "../../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}))
	tw.Write([]byte("x"))
	must(t, tw.Close())
	return buf.Bytes()
}

func TestValidateRejectsCrafted(t *testing.T) {
	a := rawEntry{"project/a.c", []byte("aaaa")}
	b := rawEntry{"project/b.c", []byte("bbbb")}
	evil := maliciousPackage(t)
	evilDigest := digestOfBytes(evil)
	withEvilPackage := func(m *manifest.Manifest) {
		m.Packages = []manifest.Package{{Name: "evil", Platform: "linux-x64", Digest: evilDigest, Size: int64(len(evil))}}
	}
	evilEntry := rawEntry{"packages/" + strings.TrimPrefix(evilDigest, "sha256:") + ".tar", evil}

	tests := map[string]struct {
		entries []rawEntry
		want    string
	}{
		"manifest not first": {[]rawEntry{a, manifestFor(t, nil, a)}, "first entry"},
		"too new":            {[]rawEntry{{manifest.EntryName, []byte(`{"formatVersion": 2}`)}}, "newer"},
		"extra entry":        {[]rawEntry{manifestFor(t, nil, a), a, b}, "unexpected"},
		"duplicate entry":    {[]rawEntry{manifestFor(t, nil, a), a, a}, "unexpected or duplicate"},
		"missing entry":      {[]rawEntry{manifestFor(t, nil, a, b), a}, "incomplete"},
		"wrong content":      {[]rawEntry{manifestFor(t, nil, a), {a.name, []byte("AAAA")}}, "checksum mismatch"},
		"wrong size":         {[]rawEntry{manifestFor(t, nil, a), {a.name, []byte("aaaaa")}}, "does not match"},
		"malicious package":  {[]rawEntry{manifestFor(t, withEvilPackage), evilEntry}, "evil"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := craftSnapshot(t, tt.entries...)
			_, err := Validate(p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate err = %v, want %q", err, tt.want)
			}

			// Unpack must fail the same way and leave nothing behind.
			st := openStore(t)
			parent := t.TempDir()
			if _, err := Unpack(p, filepath.Join(parent, "restored"), st, "linux-x64"); err == nil {
				t.Fatal("Unpack accepted the archive")
			}
			if got := listDir(t, parent); len(got) != 0 {
				t.Fatalf("Unpack left %v behind", got)
			}
		})
	}
}

func TestUnpackTooNewIsTyped(t *testing.T) {
	p := craftSnapshot(t, rawEntry{manifest.EntryName, []byte(`{"formatVersion": 9}`)})
	_, err := Unpack(p, filepath.Join(t.TempDir(), "x"), openStore(t), "linux-x64")
	var tooNew *manifest.TooNewError
	if !errors.As(err, &tooNew) {
		t.Fatalf("err = %v, want *manifest.TooNewError", err)
	}
}

func TestUnpackRefusesExistingDest(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "abc.slepok")
	_, err := Pack(dst, options(t))
	must(t, err)
	dest := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dest, "mine.txt"), []byte("mine"), 0o644))
	if _, err := Unpack(dst, dest, openStore(t), "linux-x64"); err == nil {
		t.Fatal("Unpack into existing directory succeeded")
	}
	if got := listDir(t, dest); len(got) != 1 {
		t.Fatalf("existing directory modified: %v", got)
	}
}

// TestRealSnapshot packs and restores a real environment. Every
// subdirectory of SLEPOK_REAL_ENV becomes a package; results are kept in
// SLEPOK_REAL_OUT (snapshot, store, restored project, env/ view of the store):
//
//	SLEPOK_REAL_ENV=prototype/work/env SLEPOK_REAL_PROJECT=prototype/blink SLEPOK_REAL_OUT=/tmp/x go test -run TestRealSnapshot ./internal/snapshot
func TestRealSnapshot(t *testing.T) {
	envDir, projectDir, out := os.Getenv("SLEPOK_REAL_ENV"), os.Getenv("SLEPOK_REAL_PROJECT"), os.Getenv("SLEPOK_REAL_OUT")
	if envDir == "" || projectDir == "" || out == "" {
		t.Skip("SLEPOK_REAL_ENV, SLEPOK_REAL_PROJECT and SLEPOK_REAL_OUT not set")
	}
	opt := Options{
		CreatedBy:  "slepok-test",
		Profile:    manifest.Profile{ID: "rp2040", Version: "proto"},
		Platforms:  []string{manifest.HostPlatform()},
		ProjectDir: projectDir,
	}
	must(t, filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(projectDir, p)
		opt.ProjectFiles = append(opt.ProjectFiles, filepath.ToSlash(rel))
		return nil
	}))
	entries, err := os.ReadDir(envDir)
	must(t, err)
	for _, e := range entries {
		opt.Packages = append(opt.Packages, PackageSource{
			Name: e.Name(), Platform: manifest.HostPlatform(), Dir: filepath.Join(envDir, e.Name()),
		})
	}

	must(t, os.MkdirAll(out, 0o755))
	dst := filepath.Join(out, "real.slepok")
	start := time.Now()
	m, err := Pack(dst, opt)
	must(t, err)
	info, _ := os.Stat(dst)
	t.Logf("pack: %v, %d MiB", time.Since(start).Round(time.Second), info.Size()>>20)

	start = time.Now()
	_, err = Validate(dst)
	must(t, err)
	t.Logf("validate: %v", time.Since(start).Round(time.Second))

	st, err := store.Open(filepath.Join(out, "store"))
	must(t, err)
	start = time.Now()
	_, err = Unpack(dst, filepath.Join(out, "restored"), st, manifest.HostPlatform())
	must(t, err)
	t.Logf("unpack: %v", time.Since(start).Round(time.Second))

	// An env/ directory of symlinks into the store, in the layout
	// prototype/build.sh expects.
	must(t, os.MkdirAll(filepath.Join(out, "env"), 0o755))
	for _, p := range m.Packages {
		dir, _ := st.Path(p.Digest)
		must(t, os.Symlink(dir, filepath.Join(out, "env", p.Name)))
	}
}

func TestUnpackPlatformMismatch(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "abc.slepok")
	_, err := Pack(dst, options(t))
	must(t, err)
	st := openStore(t)
	parent := t.TempDir()
	_, err = Unpack(dst, filepath.Join(parent, "restored"), st, "windows-x64")
	if err == nil || !strings.Contains(err.Error(), "windows-x64") {
		t.Fatalf("err = %v, want platform error", err)
	}
	if got := listDir(t, parent); len(got) != 0 {
		t.Fatalf("Unpack left %v behind", got)
	}
}

func TestAddEntryRejectsChangedInput(t *testing.T) {
	for _, content := range []string{"abc-extra", "ab", "xyz"} {
		t.Run(content, func(t *testing.T) {
			tw := tar.NewWriter(io.Discard)
			err := addEntry(tw, "project/a", 0o644, 3, digestOfBytes([]byte("abc")), strings.NewReader(content))
			if err == nil || !strings.Contains(err.Error(), "changed while packing") {
				t.Fatalf("changed input accepted: %v", err)
			}
		})
	}
}

type fullDiskWriter struct{}

func (fullDiskWriter) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

func TestWritePropagatesDiskFull(t *testing.T) {
	opt := options(t)
	m, err := plan(opt)
	must(t, err)
	data, err := m.Marshal()
	must(t, err)
	if err := write(fullDiskWriter{}, m, data, opt); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("write = %v, want disk-full error", err)
	}
}
