// Package store is the local content-addressed package store (spec 9.6).
//
// Layout under the store root:
//
//	packages/<hex>/         extracted, read-only package trees
//	packages/.install-*/    in-progress installs
//	archives/<hex>         canonical tar packages retained without extraction
//
// Installs are staged next to their final location: moving a directory to
// another parent needs write permission on the directory itself, which a
// read-only package tree does not have.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/pkgtar"
)

type Store struct{ root string }

// DefaultRoot returns $SLEPOK_HOME, or the per-user data directory.
func DefaultRoot() (string, error) {
	if dir := os.Getenv("SLEPOK_HOME"); dir != "" {
		return dir, nil
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return filepath.Join(dir, "slepok"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "AppData", "Local", "slepok"), nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "slepok"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "slepok"), nil
}

// Open opens the store at root, creating it if needed.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = abs
	if err := os.MkdirAll(filepath.Join(root, "packages"), 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func hexOf(digest string) (string, error) {
	h, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid digest %q", digest)
	}
	return h, nil
}

// Root returns the store directory.
func (s *Store) Root() string { return s.root }

// TempDir creates a scratch directory inside the store, on the same file
// system as the packages. The caller removes it.
func (s *Store) TempDir(pattern string) (string, error) {
	dir := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, pattern)
}

// InstallDir stores the tree at dir as a package and returns its digest.
// If want is not empty, the tree must have exactly that digest.
func (s *Store) InstallDir(dir, want string) (string, error) {
	return s.InstallDirContext(context.Background(), dir, want)
}

func (s *Store) InstallDirContext(ctx context.Context, dir, want string) (string, error) {
	digest, _, err := pkgtar.Write(contextio.Writer{Context: ctx, Writer: io.Discard}, dir)
	if err != nil {
		return "", err
	}
	if want != "" && digest != want {
		return "", fmt.Errorf("package digest mismatch: got %s, want %s", digest, want)
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		_, _, err := pkgtar.Write(contextio.Writer{Context: ctx, Writer: pw}, dir)
		pw.CloseWithError(err)
	}()
	if err := s.InstallContext(ctx, digest, pr); err != nil {
		pr.CloseWithError(err)
		return "", err
	}
	return digest, nil
}

// RemoveTree deletes a directory tree that may contain read-only entries.
func RemoveTree(dir string) error { return removeTree(dir) }

// Path returns where the package with the given digest lives.
func (s *Store) Path(digest string) (string, error) {
	h, err := hexOf(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "packages", h), nil
}

// Has reports whether a package is available as a tree or canonical archive.
func (s *Store) Has(digest string) bool {
	p, err := s.Path(digest)
	if err != nil {
		return false
	}
	if info, err := os.Lstat(p); err == nil && info.IsDir() {
		return true
	}
	p, err = s.ArchivePath(digest)
	if err != nil {
		return false
	}
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular()
}

// Install extracts the package tar read from r and makes it available
// under its digest. The package appears only after the bytes read match
// digest. If the package is already installed, r is consumed and checked
// but nothing is written.
func (s *Store) Install(digest string, r io.Reader) error {
	return s.InstallContext(context.Background(), digest, r)
}

// InstallContext is Install with cancellation while waiting for another installer.
func (s *Store) InstallContext(ctx context.Context, digest string, r io.Reader) error {
	final, err := s.Path(digest)
	if err != nil {
		return err
	}
	guard, err := s.lock(ctx, "package-"+filepath.Base(final), false)
	if err != nil {
		return err
	}
	defer guard.Close()
	h := sha256.New()
	tee := io.TeeReader(contextio.Reader{Context: ctx, Reader: r}, h)

	if s.HasTree(digest) {
		if _, err := io.Copy(io.Discard, tee); err != nil {
			return err
		}
		return checkDigest(digest, h.Sum(nil))
	}

	staged := filepath.Join(filepath.Dir(final), ".install-"+rand.Text())
	defer removeTree(staged)

	if err := pkgtar.Extract(tee, staged); err != nil {
		return fmt.Errorf("package %s: %w", digest, err)
	}
	if err := checkDigest(digest, h.Sum(nil)); err != nil {
		return err
	}
	if err := setWritable(staged, false); err != nil {
		return err
	}
	if err := os.Rename(staged, final); err != nil {
		if s.HasTree(digest) {
			return nil // installed concurrently
		}
		return err
	}
	return nil
}

func checkDigest(want string, sum []byte) error {
	if got := pkgtar.Digest(sum); got != want {
		return fmt.Errorf("package checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// Remove deletes an installed package.
func (s *Store) Remove(digest string) error {
	p, err := s.Path(digest)
	if err != nil {
		return err
	}
	if err := removeTree(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	archive, err := s.ArchivePath(digest)
	if err != nil {
		return err
	}
	if err := removeTree(archive); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removeTree deletes a tree that may contain read-only entries.
func removeTree(dir string) error {
	if err := setWritable(dir, true); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.RemoveAll(dir)
}

// setWritable adds or removes write permission on every file and directory
// in the tree. Directories are visited before their contents, which is the
// order needed to restore write access.
func setWritable(dir string, writable bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if writable {
			mode |= 0o200
		} else {
			mode &^= 0o222
		}
		return os.Chmod(p, mode)
	})
}
