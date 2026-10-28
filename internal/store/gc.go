package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Grace periods protect data that a concurrent slepok process may still be
// writing: a package installed by a running init is not in any lock yet.
const (
	packageGrace = time.Hour
	tmpGrace     = time.Hour
	logRetention = 14 * 24 * time.Hour
)

// Usage summarises one area of the store.
type Usage struct {
	Count int
	Bytes int64
}

// Item is something garbage collection would delete.
type Item struct {
	Kind  string // "package", "download", "tmp", "log"
	Name  string
	Path  string
	Bytes int64
}

// Packages returns the digests of the installed packages.
func (s *Store) Packages() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "packages"))
	if err != nil {
		return nil, err
	}
	var digests []string
	for _, e := range entries {
		if _, err := hexOf("sha256:" + e.Name()); err == nil && e.IsDir() {
			digests = append(digests, "sha256:"+e.Name())
		}
	}
	return digests, nil
}

// Usage reports the size of each area: packages, downloads, tmp and logs.
func (s *Store) Usage() (map[string]Usage, error) {
	out := map[string]Usage{}
	for _, area := range []string{"packages", "downloads", "tmp", "logs"} {
		entries, err := os.ReadDir(filepath.Join(s.root, area))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		u := Usage{Count: len(entries)}
		for _, e := range entries {
			n, err := treeSize(filepath.Join(s.root, area, e.Name()))
			if err != nil {
				return nil, err
			}
			u.Bytes += n
		}
		out[area] = u
	}
	return out, nil
}

// PlanGC lists what garbage collection deletes: packages not in keep,
// downloads whose sha256 is not in keepDownloads, and leftovers of
// interrupted operations. Recent entries are kept even when unreferenced.
func (s *Store) PlanGC(keep, keepDownloads map[string]bool, now time.Time) ([]Item, error) {
	var items []Item
	add := func(kind, name, p string, minAge time.Duration) error {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < minAge {
			return nil
		}
		n, err := treeSize(p)
		if err != nil {
			return err
		}
		items = append(items, Item{Kind: kind, Name: name, Path: p, Bytes: n})
		return nil
	}

	pkgDir := filepath.Join(s.root, "packages")
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		p := filepath.Join(pkgDir, e.Name())
		switch {
		case strings.HasPrefix(e.Name(), ".install-"):
			err = add("tmp", e.Name(), p, tmpGrace)
		case !keep["sha256:"+e.Name()]:
			err = add("package", "sha256:"+e.Name(), p, packageGrace)
		}
		if err != nil {
			return nil, err
		}
	}

	for _, area := range []struct {
		dir, kind string
		minAge    time.Duration
		include   func(name string) bool
	}{
		{"downloads", "download", 0, func(n string) bool { return !keepDownloads[strings.TrimSuffix(n, ".part")] }},
		{"tmp", "tmp", tmpGrace, func(string) bool { return true }},
		{"logs", "log", logRetention, func(string) bool { return true }},
	} {
		dir := filepath.Join(s.root, area.dir)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !area.include(e.Name()) {
				continue
			}
			if err := add(area.kind, e.Name(), filepath.Join(dir, e.Name()), area.minAge); err != nil {
				return nil, err
			}
		}
	}
	return items, nil
}

// Delete removes the items of a garbage collection plan.
func (s *Store) Delete(items []Item) error {
	for _, it := range items {
		if !strings.HasPrefix(it.Path, s.root+string(filepath.Separator)) {
			return errors.New("refusing to delete outside the store: " + it.Path)
		}
		if err := removeTree(it.Path); err != nil {
			return err
		}
	}
	return nil
}

// treeSize is the apparent size of a file or directory tree; hardlinked
// files are counted once per link.
func treeSize(p string) (int64, error) {
	var n int64
	err := filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			n += info.Size()
		}
		return nil
	})
	return n, err
}
