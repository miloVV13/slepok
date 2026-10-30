package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/pkgtar"
)

// ArchivePath addresses a canonical tar kept without extraction. This lets
// a host retain foreign packages without interpreting their filesystem metadata.
func (s *Store) ArchivePath(digest string) (string, error) {
	h, err := hexOf(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "archives", h), nil
}

// HasTree reports a runnable, extracted package rather than an archive.
func (s *Store) HasTree(digest string) bool {
	p, err := s.Path(digest)
	if err != nil {
		return false
	}
	info, err := os.Lstat(p)
	return err == nil && info.IsDir()
}

// InstallArchiveContext validates both the package structure and digest before
// atomically publishing it. It shares the package lock with extracted installs.
func (s *Store) InstallArchiveContext(ctx context.Context, digest string, r io.Reader) error {
	final, err := s.ArchivePath(digest)
	if err != nil {
		return err
	}
	guard, err := s.lock(ctx, "package-"+filepath.Base(final), false)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(final), ".install-")
	if err != nil {
		return err
	}
	defer removeTree(f.Name())
	defer f.Close()
	h := sha256.New()
	tee := io.TeeReader(contextio.Reader{Context: ctx, Reader: r}, io.MultiWriter(f, h))
	if err := pkgtar.Verify(tee); err != nil {
		return fmt.Errorf("package %s: %w", digest, err)
	}
	if err := checkDigest(digest, h.Sum(nil)); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Chmod(0444); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Has(digest) {
		return nil
	}
	return os.Rename(f.Name(), final)
}
