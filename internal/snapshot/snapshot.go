// Package snapshot creates, validates and restores snapshot files
// (spec 9.1, 9.4-9.7).
//
// A snapshot is a zstd stream containing a tar archive whose first entry is
// manifest.json. Every other entry is listed in the manifest with its size
// and sha256 digest.
package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/safepath"
	"github.com/miloVV13/slepok/internal/store"
)

const reportEntry = "reports/verify.json"

var epoch = time.Unix(0, 0).UTC()

// PackageSource is an environment package to include, read from Dir.
type PackageSource struct {
	Name     string
	Version  string
	Platform string
	License  string
	// SourceCode and Notice are copied into the manifest (spec 15.1).
	SourceCode string
	Notice     string
	Dir        string
}

// Extra is an additional file stored outside project/, such as a NOTICE
// under reports/ or bundled source code under sources/. Either Data or
// Path, a file on disk, holds the content.
type Extra struct {
	Name string
	Data []byte
	Path string
}

func (e Extra) open() (io.ReadCloser, error) {
	if e.Path != "" {
		return os.Open(e.Path)
	}
	return io.NopCloser(bytes.NewReader(e.Data)), nil
}

type Options struct {
	Context   context.Context
	CreatedBy string
	Profile   manifest.Profile
	Platforms []string
	// ProjectDir holds the project; ProjectFiles are slash-separated paths
	// relative to it. Only regular files are supported.
	ProjectDir   string
	ProjectFiles []string
	Packages     []PackageSource
	// Report, if set, is stored as reports/verify.json.
	Report []byte
	Extras []Extra
}

// Pack writes a snapshot to dst, which must not exist. The file is written
// under a temporary name, validated, and only then given its final name.
func Pack(dst string, opt Options) (*manifest.Manifest, error) {
	if err := mustNotExist(dst); err != nil {
		return nil, err
	}
	if err := contextio.OrBackground(opt.Context).Err(); err != nil {
		return nil, err
	}
	m, err := plan(opt)
	if err != nil {
		return nil, err
	}
	data, err := m.Marshal()
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	if err := write(tmp, m, data, opt); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if _, err := ValidateContext(opt.Context, tmp.Name()); err != nil {
		return nil, fmt.Errorf("written snapshot failed validation: %w", err)
	}
	// CreateTemp makes the file private; a snapshot is meant to be shared.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return nil, err
	}
	if err := contextio.OrBackground(opt.Context).Err(); err != nil {
		return nil, err
	}
	// Link fails if dst appeared in the meantime, unlike Rename.
	if err := os.Link(tmp.Name(), dst); err != nil {
		return nil, err
	}
	return m, nil
}

// plan hashes every input and builds the manifest.
func plan(opt Options) (*manifest.Manifest, error) {
	m := &manifest.Manifest{
		FormatVersion: manifest.FormatVersion,
		CreatedBy:     opt.CreatedBy,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		Profile:       opt.Profile,
		Platforms:     opt.Platforms,
	}
	files := slices.Clone(opt.ProjectFiles)
	slices.Sort(files)
	for _, rel := range files {
		f, err := projectFileContext(opt.Context, opt.ProjectDir, rel)
		if err != nil {
			return nil, err
		}
		m.Files = append(m.Files, f)
	}
	if opt.Report != nil {
		m.Files = append(m.Files, manifest.File{
			Path:   reportEntry,
			Size:   int64(len(opt.Report)),
			Digest: digestOf(opt.Report),
		})
	}
	for _, e := range opt.Extras {
		f, err := extraFileContext(opt.Context, e)
		if err != nil {
			return nil, err
		}
		m.Files = append(m.Files, f)
	}
	for _, p := range opt.Packages {
		digest, size, err := pkgtar.Write(contextio.Writer{Context: opt.Context, Writer: io.Discard}, p.Dir)
		if err != nil {
			return nil, fmt.Errorf("package %s: %w", p.Name, err)
		}
		m.Packages = append(m.Packages, manifest.Package{
			Name: p.Name, Version: p.Version, Platform: p.Platform, License: p.License,
			SourceCode: p.SourceCode, Notice: p.Notice, Digest: digest, Size: size,
		})
	}
	return m, nil
}

func extraFileContext(ctx context.Context, e Extra) (manifest.File, error) {
	r, err := e.open()
	if err != nil {
		return manifest.File{}, err
	}
	defer r.Close()
	h := sha256.New()
	n, err := io.Copy(h, contextio.Reader{Context: ctx, Reader: r})
	if err != nil {
		return manifest.File{}, err
	}
	return manifest.File{Path: e.Name, Size: n, Digest: pkgtar.Digest(h.Sum(nil))}, nil
}

func projectFileContext(ctx context.Context, dir, rel string) (manifest.File, error) {
	if err := safepath.Check(rel); err != nil {
		return manifest.File{}, fmt.Errorf("project file: %w", err)
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Lstat(p)
	if err != nil {
		return manifest.File{}, err
	}
	if !info.Mode().IsRegular() {
		return manifest.File{}, fmt.Errorf("project file %s: only regular files are supported, found %v", rel, info.Mode().Type())
	}
	f, err := os.Open(p)
	if err != nil {
		return manifest.File{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, contextio.Reader{Context: ctx, Reader: f})
	if err != nil {
		return manifest.File{}, err
	}
	return manifest.File{
		Path:       "project/" + rel,
		Size:       n,
		Digest:     pkgtar.Digest(h.Sum(nil)),
		Executable: info.Mode().Perm()&0o111 != 0,
	}, nil
}

func write(w io.Writer, m *manifest.Manifest, data []byte, opt Options) error {
	zw, err := zstd.NewWriter(contextio.Writer{Context: opt.Context, Writer: w}, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		return err
	}
	defer zw.Close()
	tw := tar.NewWriter(zw)

	if err := addEntry(tw, manifest.EntryName, 0o644, int64(len(data)), digestOf(data), bytes.NewReader(data)); err != nil {
		return err
	}
	for _, f := range m.Files {
		if err := addFile(tw, f, opt); err != nil {
			return err
		}
	}
	for i, p := range m.Packages {
		hdr := &tar.Header{Name: p.Entry(), Typeflag: tar.TypeReg, Mode: 0o644, Size: p.Size, ModTime: epoch, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		digest, size, err := pkgtar.Write(contextio.Writer{Context: opt.Context, Writer: tw}, opt.Packages[i].Dir)
		if err != nil {
			return fmt.Errorf("package %s: %w", p.Name, err)
		}
		if digest != p.Digest || size != p.Size {
			return fmt.Errorf("package %s changed while packing", p.Name)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

func addFile(tw *tar.Writer, f manifest.File, opt Options) error {
	mode := int64(0o644)
	if f.Executable {
		mode = 0o755
	}
	if f.Path == reportEntry {
		return addEntry(tw, f.Path, mode, f.Size, f.Digest, bytes.NewReader(opt.Report))
	}
	for _, e := range opt.Extras {
		if e.Name == f.Path {
			r, err := e.open()
			if err != nil {
				return err
			}
			defer r.Close()
			return addEntry(tw, f.Path, mode, f.Size, f.Digest, contextio.Reader{Context: opt.Context, Reader: r})
		}
	}
	rel := strings.TrimPrefix(f.Path, "project/")
	src, err := os.Open(filepath.Join(opt.ProjectDir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	defer src.Close()
	return addEntry(tw, f.Path, mode, f.Size, f.Digest, contextio.Reader{Context: opt.Context, Reader: src})
}

// addEntry writes a regular file entry and checks that the content still
// matches what was hashed when the manifest was planned.
func addEntry(tw *tar.Writer, name string, mode, size int64, digest string, r io.Reader) error {
	hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: size, ModTime: epoch, Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tw, h), io.LimitReader(r, size))
	if err != nil {
		return err
	}
	if n != size || pkgtar.Digest(h.Sum(nil)) != digest {
		return fmt.Errorf("%s changed while packing", name)
	}
	// A growing input can keep the same prefix and digest. Check EOF too.
	var extra [1]byte
	if n, err := io.ReadFull(r, extra[:]); n != 0 {
		return fmt.Errorf("%s changed while packing: expected end of file", name)
	} else if err != io.EOF {
		return fmt.Errorf("read %s: %w", name, err)
	}
	return nil
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return pkgtar.Digest(sum[:])
}

// handler receives the content of each entry. It may stop reading early;
// the rest is drained and the digest is checked after it returns.
type handler interface {
	file(f manifest.File, r io.Reader) error
	pkg(p manifest.Package, r io.Reader) error
}

// Validate checks the snapshot at src without writing anything (spec 9.4):
// structure, the manifest, every size and digest, and the safety rules of
// every package.
func Validate(src string) (*manifest.Manifest, error) {
	return ValidateContext(context.Background(), src)
}

func ValidateContext(ctx context.Context, src string) (*manifest.Manifest, error) {
	return read(ctx, src, nil, verifier{})
}

// ReadManifest returns the manifest of the snapshot without reading the
// rest of the archive. The entries are not verified.
func ReadManifest(src string) (*manifest.Manifest, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return readManifest(tar.NewReader(zr))
}

func readManifest(tr *tar.Reader) (*manifest.Manifest, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("not a snapshot: %w", err)
	}
	if hdr.Name != manifest.EntryName || hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("not a snapshot: first entry is %q, want %s", hdr.Name, manifest.EntryName)
	}
	if hdr.Size > manifest.MaxSize {
		return nil, fmt.Errorf("manifest is too large (%d bytes)", hdr.Size)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return manifest.Parse(data)
}

type expectation struct {
	file *manifest.File
	pkg  *manifest.Package
}

func (e expectation) size() int64 {
	if e.file != nil {
		return e.file.Size
	}
	return e.pkg.Size
}

func (e expectation) digest() string {
	if e.file != nil {
		return e.file.Digest
	}
	return e.pkg.Digest
}

func read(ctx context.Context, src string, check func(*manifest.Manifest) error, h handler) (*manifest.Manifest, error) {
	ctx = contextio.OrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(contextio.Reader{Context: ctx, Reader: f})
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	m, err := readManifest(tr)
	if err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(m); err != nil {
			return nil, err
		}
	}

	expected := map[string]expectation{}
	for i := range m.Files {
		expected[m.Files[i].Path] = expectation{file: &m.Files[i]}
	}
	for i := range m.Packages {
		expected[m.Packages[i].Entry()] = expectation{pkg: &m.Packages[i]}
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read snapshot: %w", err)
		}
		e, ok := expected[hdr.Name]
		if !ok {
			return nil, fmt.Errorf("unexpected or duplicate entry %q", hdr.Name)
		}
		delete(expected, hdr.Name)
		if hdr.Typeflag != tar.TypeReg || hdr.Size != e.size() {
			return nil, fmt.Errorf("%s: entry does not match manifest", hdr.Name)
		}

		hs := sha256.New()
		r := io.TeeReader(contextio.Reader{Context: ctx, Reader: tr}, hs)
		if e.file != nil {
			err = h.file(*e.file, r)
		} else {
			err = h.pkg(*e.pkg, r)
		}
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		if pkgtar.Digest(hs.Sum(nil)) != e.digest() {
			return nil, fmt.Errorf("%s: checksum mismatch", hdr.Name)
		}
	}
	if len(expected) > 0 {
		missing := slices.Sorted(func(yield func(string) bool) {
			for name := range expected {
				if !yield(name) {
					return
				}
			}
		})
		return nil, fmt.Errorf("snapshot is incomplete: %d entries missing, first %s", len(missing), missing[0])
	}
	// Reading to the end also checks the zstd frame checksum.
	if err := expectZeros(zr); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

func expectZeros(r io.Reader) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if slices.ContainsFunc(buf[:n], func(b byte) bool { return b != 0 }) {
			return fmt.Errorf("unexpected data after end of archive")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read snapshot: %w", err)
		}
	}
}

type verifier struct{}

func (verifier) file(manifest.File, io.Reader) error { return nil }
func (verifier) pkg(p manifest.Package, r io.Reader) error {
	if err := pkgtar.Verify(r); err != nil {
		return fmt.Errorf("package %s: %w", p.Name, err)
	}
	return nil
}

// Unpack restores the snapshot at src. Packages for platform are installed
// into st; the project is restored into dest, which must not exist. The
// project directory appears only after the whole snapshot has been
// verified. Packages installed before a failure stay in the store: they
// were verified individually and are addressed by content.
func Unpack(src, dest string, st *store.Store, platform string) (*manifest.Manifest, error) {
	return UnpackContext(context.Background(), src, dest, st, platform)
}

func UnpackContext(ctx context.Context, src, dest string, st *store.Store, platform string) (*manifest.Manifest, error) {
	if err := contextio.OrBackground(ctx).Err(); err != nil {
		return nil, err
	}
	if err := mustNotExist(dest); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".slepok-tmp-")
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(staging)
		}
	}()
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	checkPlatform := func(m *manifest.Manifest) error {
		if !slices.Contains(m.Platforms, platform) {
			return fmt.Errorf("snapshot contains packages for %s, this machine is %s",
				strings.Join(m.Platforms, ", "), platform)
		}
		return nil
	}
	m, err := read(ctx, src, checkPlatform, &unpacker{root: root, store: st, platform: platform})
	if err != nil {
		return nil, err
	}
	if err := root.Close(); err != nil {
		return nil, err
	}
	if err := contextio.OrBackground(ctx).Err(); err != nil {
		return nil, err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return nil, err
	}
	if err := mustNotExist(dest); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, dest); err != nil {
		return nil, err
	}
	done = true
	return m, nil
}

type unpacker struct {
	root     *os.Root
	store    *store.Store
	platform string
}

func (u *unpacker) file(f manifest.File, r io.Reader) error {
	rel, ok := strings.CutPrefix(f.Path, "project/")
	if !ok {
		return nil // reports are verified but not restored
	}
	if dir := path.Dir(rel); dir != "." {
		if err := u.root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	mode := fs.FileMode(0o644)
	if f.Executable {
		mode = 0o755
	}
	out, err := u.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (u *unpacker) pkg(p manifest.Package, r io.Reader) error {
	if p.Platform != u.platform && p.Platform != manifest.AnyPlatform {
		return verifier{}.pkg(p, r)
	}
	if err := u.store.Install(p.Digest, r); err != nil {
		return fmt.Errorf("package %s: %w", p.Name, err)
	}
	return nil
}

func mustNotExist(p string) error {
	_, err := os.Lstat(p)
	if err == nil {
		return fmt.Errorf("%s already exists", p)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
