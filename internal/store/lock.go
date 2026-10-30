package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/miloVV13/slepok/internal/filelock"
)

// Acquire protects a complete CLI operation, including publishing its lock
// and registry entry. GC takes an exclusive lock before planning deletion;
// other store users take a shared lock. Close releases it.
func (s *Store) Acquire(ctx context.Context, exclusive bool) (*os.File, error) {
	return s.lock(ctx, "operations", !exclusive)
}

// LockDownload serializes a download and its resumable .part file across
// processes. Different checksums can still download concurrently.
func (s *Store) LockDownload(ctx context.Context, sum string) (*os.File, error) {
	h, err := hexOf("sha256:" + sum)
	if err != nil {
		return nil, err
	}
	return s.lock(ctx, "download-"+h, false)
}

// LockProject serializes initialization of the same directory, including
// symlink aliases. The directory must already exist.
func (s *Store) LockProject(ctx context.Context, dir string) (*os.File, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return s.lock(ctx, fmt.Sprintf("project-%x", sha256.Sum256([]byte(real))), false)
}

func (s *Store) lock(ctx context.Context, name string, shared bool) (*os.File, error) {
	dir := filepath.Join(s.root, ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return filelock.Acquire(ctx, filepath.Join(dir, name), shared)
}
