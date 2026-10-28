// Package pkgtar writes and reads canonical package tarballs (spec 9.2).
//
// A package is a directory tree stored as a tar stream with a fixed entry
// order, fixed timestamps and ownership and normalised permissions, so the
// same tree always produces the same bytes and therefore the same digest.
package pkgtar

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/miloVV13/slepok/internal/safepath"
)

const (
	dirMode  = 0o755
	execMode = 0o755
	fileMode = 0o644
	linkMode = 0o777
)

var epoch = time.Unix(0, 0).UTC()

// Digest formats a sha256 sum the way manifests store it.
func Digest(sum []byte) string { return "sha256:" + hex.EncodeToString(sum) }

// Write writes the tree rooted at dir to w as a canonical package tar. It
// returns the digest and size of the bytes written.
func Write(w io.Writer, dir string) (digest string, size int64, err error) {
	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(w, h)}
	tw := tar.NewWriter(cw)
	firstLink := map[fileID]string{}

	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if err := safepath.Check(name); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: name, ModTime: epoch, Format: tar.FormatPAX}

		switch mode := info.Mode(); {
		case mode.IsDir():
			hdr.Typeflag, hdr.Mode = tar.TypeDir, dirMode
			return tw.WriteHeader(hdr)
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			target = filepath.ToSlash(target)
			if err := safepath.CheckLinkTarget(name, target); err != nil {
				return err
			}
			hdr.Typeflag, hdr.Mode, hdr.Linkname = tar.TypeSymlink, linkMode, target
			return tw.WriteHeader(hdr)
		case mode.IsRegular():
			hdr.Mode = normMode(int64(mode.Perm()))
			if id, ok := hardlinkID(info); ok {
				if first, seen := firstLink[id]; seen {
					hdr.Typeflag, hdr.Linkname = tar.TypeLink, first
					return tw.WriteHeader(hdr)
				}
				firstLink[id] = name
			}
			hdr.Typeflag, hdr.Size = tar.TypeReg, info.Size()
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			return copyFile(tw, p, info.Size())
		default:
			return fmt.Errorf("%s: unsupported file type %v", name, mode.Type())
		}
	})
	if err != nil {
		return "", 0, err
	}
	if err := tw.Close(); err != nil {
		return "", 0, err
	}
	return Digest(h.Sum(nil)), cw.n, nil
}

func copyFile(w io.Writer, p string, size int64) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.CopyN(w, f, size); err != nil {
		return fmt.Errorf("%s: %w (file changed while packing?)", p, err)
	}
	return nil
}

// Extract unpacks the package tar read from r into dest, which must not
// exist yet. It enforces the same rules as Verify and reads r to EOF.
func Extract(r io.Reader, dest string) error {
	if err := os.Mkdir(dest, dirMode); err != nil {
		return err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	return walk(r, rootSink{root})
}

// Verify checks the package tar read from r against the package rules
// without writing anything. It reads r to EOF.
func Verify(r io.Reader) error {
	return walk(r, discardSink{})
}

type sink interface {
	dir(name string) error
	file(name string, mode fs.FileMode, r io.Reader) error
	symlink(name, target string) error
	link(name, target string) error
}

func walk(r io.Reader, s sink) error {
	tr := tar.NewReader(r)
	dirs := map[string]bool{}
	files := map[string]bool{}
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read package: %w", err)
		}
		name := hdr.Name
		if hdr.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if err := safepath.Check(name); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("%s: duplicate entry", name)
		}
		seen[name] = true
		// Parents must be directories created earlier by this archive. This
		// also rules out writing through a symlink.
		if parent := path.Dir(name); parent != "." && !dirs[parent] {
			return fmt.Errorf("%s: parent is not a directory entry of the package", name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			dirs[name] = true
			err = s.dir(name)
		case tar.TypeReg:
			files[name] = true
			err = s.file(name, fs.FileMode(normMode(hdr.Mode)), tr)
		case tar.TypeSymlink:
			if err := safepath.CheckLinkTarget(name, hdr.Linkname); err != nil {
				return err
			}
			err = s.symlink(name, hdr.Linkname)
		case tar.TypeLink:
			if !files[hdr.Linkname] {
				return fmt.Errorf("%s: hardlink target %q is not an earlier regular file", name, hdr.Linkname)
			}
			err = s.link(name, hdr.Linkname)
		default:
			return fmt.Errorf("%s: unsupported entry type %q", name, hdr.Typeflag)
		}
		if err != nil {
			return err
		}
	}
	// Drain the end-of-archive padding so callers hashing r see every byte.
	_, err := io.Copy(io.Discard, r)
	return err
}

func normMode(m int64) int64 {
	if m&0o111 != 0 {
		return execMode
	}
	return fileMode
}

type rootSink struct{ root *os.Root }

func (s rootSink) dir(name string) error { return s.root.Mkdir(name, dirMode) }

func (s rootSink) file(name string, mode fs.FileMode, r io.Reader) error {
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// OpenFile is subject to the umask; set the canonical mode explicitly.
	return s.root.Chmod(name, mode)
}

func (s rootSink) symlink(name, target string) error { return s.root.Symlink(target, name) }
func (s rootSink) link(name, target string) error    { return s.root.Link(target, name) }

type discardSink struct{}

func (discardSink) dir(string) error { return nil }
func (discardSink) file(_ string, _ fs.FileMode, r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}
func (discardSink) symlink(string, string) error { return nil }
func (discardSink) link(string, string) error    { return nil }

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
