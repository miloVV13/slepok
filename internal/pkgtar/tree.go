package pkgtar

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/safepath"
)

// Tree stores Unix metadata in memory and file contents in opaque temporary
// files. Package names, permissions and links never reach the host filesystem.
// Its canonical output is identical to Write on a Unix filesystem.
type Tree struct {
	dir     string
	entries map[string]*treeEntry
}

type treeEntry struct {
	kind       byte
	mode       int64
	link, file string
	size       int64
}

// NewTree uses an existing private scratch directory, owned by the caller.
func NewTree(dir string) *Tree { return &Tree{dir: dir, entries: map[string]*treeEntry{}} }

func (t *Tree) parent(name string) error {
	if err := safepath.Check(name); err != nil {
		return err
	}
	for p := path.Dir(name); p != "."; p = path.Dir(p) {
		if e := t.entries[p]; e != nil && e.kind != tar.TypeDir {
			return fmt.Errorf("%s: parent %s is not a directory", name, p)
		}
		t.entries[p] = &treeEntry{kind: tar.TypeDir}
	}
	return nil
}

func (t *Tree) put(name string, e *treeEntry) error {
	if err := t.parent(name); err != nil {
		return err
	}
	if old := t.entries[name]; old != nil {
		if old.kind == tar.TypeDir && e.kind == tar.TypeDir {
			return nil
		}
		return fmt.Errorf("%s: conflicting package entries", name)
	}
	t.entries[name] = e
	return nil
}

func (t *Tree) Dir(name string) error { return t.put(name, &treeEntry{kind: tar.TypeDir}) }

func (t *Tree) File(ctx context.Context, name string, mode int64, r io.Reader) error {
	if err := t.parent(name); err != nil {
		return err
	}
	f, err := os.CreateTemp(t.dir, "blob-")
	if err != nil {
		return err
	}
	n, err := io.Copy(f, contextio.Reader{Context: ctx, Reader: r})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return t.put(name, &treeEntry{kind: tar.TypeReg, mode: normMode(mode), file: f.Name(), size: n})
}

func (t *Tree) Symlink(name, target string) error {
	if err := safepath.CheckLinkTarget(name, target); err != nil {
		return err
	}
	return t.put(name, &treeEntry{kind: tar.TypeSymlink, link: target})
}

func (t *Tree) Link(name, target string) error {
	if err := safepath.Check(target); err != nil {
		return err
	}
	e := t.entries[target]
	if e == nil || e.kind != tar.TypeReg {
		return fmt.Errorf("%s: hardlink target %q is not an earlier regular file", name, target)
	}
	return t.put(name, e)
}

// Clean removes entries whose path has a component selected by remove.
func (t *Tree) Clean(remove func(string) bool) {
	for name := range t.entries {
		for _, part := range strings.Split(name, "/") {
			if remove(part) {
				delete(t.entries, name)
				break
			}
		}
	}
}

func (t *Tree) Write(ctx context.Context, w io.Writer) (string, int64, error) {
	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(contextio.Writer{Context: ctx, Writer: w}, h)}
	tw := tar.NewWriter(cw)
	children := map[string][]string{}
	for name := range t.entries {
		children[path.Dir(name)] = append(children[path.Dir(name)], name)
	}
	for _, names := range children {
		sort.Slice(names, func(i, j int) bool { return path.Base(names[i]) < path.Base(names[j]) })
	}
	first := map[*treeEntry]string{}
	var visit func(string) error
	visit = func(parent string) error {
		for _, name := range children[parent] {
			if err := ctx.Err(); err != nil {
				return err
			}
			e := t.entries[name]
			hdr := &tar.Header{Name: name, Typeflag: e.kind, ModTime: epoch, Format: tar.FormatPAX}
			switch e.kind {
			case tar.TypeDir:
				hdr.Mode = dirMode
			case tar.TypeSymlink:
				hdr.Mode, hdr.Linkname = linkMode, e.link
			case tar.TypeReg:
				hdr.Mode = e.mode
				if prev, ok := first[e]; ok {
					hdr.Typeflag, hdr.Linkname = tar.TypeLink, prev
				} else {
					first[e] = name
					hdr.Size = e.size
				}
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if hdr.Typeflag == tar.TypeReg {
				if err := copyFile(tw, e.file, e.size); err != nil {
					return err
				}
			}
			if e.kind == tar.TypeDir {
				if err := visit(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit("."); err != nil {
		return "", 0, err
	}
	if err := tw.Close(); err != nil {
		return "", 0, err
	}
	return Digest(h.Sum(nil)), cw.n, nil
}
