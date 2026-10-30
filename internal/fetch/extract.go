package fetch

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/ulikunitz/xz"

	"github.com/miloVV13/slepok/internal/contextio"
	"github.com/miloVV13/slepok/internal/safepath"
)

// extract unpacks a vendor archive into dest, which must not exist,
// dropping the first strip path components. Vendor archives do not follow
// the canonical package rules (spec 9.2), but the same safety rules apply:
// no paths outside dest, no links escaping it, no writes through links.
func extract(archive, format string, strip int, dest string) error {
	return extractContext(context.Background(), archive, format, strip, dest)
}

func extractContext(ctx context.Context, archive, format string, strip int, dest string) error {
	_, err := extractLayout(ctx, archive, format, strip, dest, false)
	return err
}

func extractLayout(ctx context.Context, archive, format string, strip int, dest string, windowsLayout bool) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var links map[string]string
	if windowsLayout {
		links = map[string]string{}
	}
	w := &writer{links: links, ctx: ctx, root: root, symlinks: map[string]bool{}, files: map[string]bool{}}

	if format == "zip" {
		return links, extractZip(archive, strip, w)
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader
	switch format {
	case "tar.gz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	case "tar.xz":
		xr, err := xz.NewReader(f)
		if err != nil {
			return nil, err
		}
		r = xr
	default:
		return nil, fmt.Errorf("unsupported archive format %q", format)
	}
	return links, extractTar(contextio.Reader{Context: ctx, Reader: r}, strip, w)
}

func extractTar(r io.Reader, strip int, w *writer) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name, ok, err := stripPath(hdr.Name, strip)
		if err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = w.dir(name)
		case tar.TypeReg:
			err = w.file(name, fs.FileMode(hdr.Mode), tr)
		case tar.TypeSymlink:
			err = w.symlink(name, hdr.Linkname)
		case tar.TypeLink:
			target, ok, serr := stripPath(hdr.Linkname, strip)
			if serr != nil || !ok {
				return fmt.Errorf("%s: invalid hardlink target %q", name, hdr.Linkname)
			}
			err = w.link(name, target)
		case tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("%s: unsupported entry type %q", name, hdr.Typeflag)
		}
		if err != nil {
			return err
		}
	}
}

func extractZip(archive string, strip int, w *writer) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name, ok, err := stripPath(f.Name, strip)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = w.dir(name)
		case mode&fs.ModeSymlink != 0:
			err = w.zipSymlink(name, f)
		case mode.IsRegular():
			var rc io.ReadCloser
			if rc, err = f.Open(); err == nil {
				err = w.file(name, mode, rc)
				rc.Close()
			}
		default:
			err = fmt.Errorf("%s: unsupported entry type %v", name, mode.Type())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// stripPath normalises an archive path and drops the first n components.
// ok is false for entries that disappear entirely, such as the top-level
// directory itself.
func stripPath(name string, n int) (string, bool, error) {
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimSuffix(name, "/")
	if name == "" || name == "." {
		return "", false, nil
	}
	// Check the whole path before stripping, so that e.g. "/etc/x" is
	// rejected instead of silently becoming "etc/x".
	if err := safepath.Check(name); err != nil {
		return "", false, err
	}
	parts := strings.Split(name, "/")
	if len(parts) <= n {
		return "", false, nil
	}
	return strings.Join(parts[n:], "/"), true, nil
}

type writer struct {
	links    map[string]string
	ctx      context.Context
	root     *os.Root
	symlinks map[string]bool
	files    map[string]bool
}

// parent creates the parent directories of name, refusing to go through a
// symlink created by this archive.
func (w *writer) parent(name string) error {
	if err := safepath.CheckForHost(name); err != nil {
		return err
	}
	if err := contextio.OrBackground(w.ctx).Err(); err != nil {
		return err
	}
	for d := path.Dir(name); d != "."; d = path.Dir(d) {
		if w.symlinks[d] {
			return fmt.Errorf("%s: path goes through symlink %s", name, d)
		}
	}
	if d := path.Dir(name); d != "." {
		return w.root.MkdirAll(d, 0o755)
	}
	return nil
}

func (w *writer) dir(name string) error {
	if err := w.parent(name); err != nil {
		return err
	}
	if w.symlinks[name] {
		return fmt.Errorf("%s: directory replaces a symlink", name)
	}
	return w.root.MkdirAll(name, 0o755)
}

func (w *writer) file(name string, mode fs.FileMode, r io.Reader) error {
	if err := w.parent(name); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := w.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, contextio.Reader{Context: w.ctx, Reader: r}); err != nil {
		f.Close()
		return err
	}
	w.files[name] = true
	return f.Close()
}

func (w *writer) symlink(name, target string) error {
	if err := safepath.CheckLinkTargetForHost(name, target); err != nil {
		return err
	}
	if err := w.parent(name); err != nil {
		return err
	}
	w.symlinks[name] = true
	if w.links != nil {
		// A placeholder reserves the name against duplicate entries without
		// requiring symlink privileges. Resolve only after the tree is complete.
		f, err := w.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(target)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		w.links[name] = target
		return nil
	}
	return w.root.Symlink(target, name)
}

func (w *writer) zipSymlink(name string, f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	target, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return err
	}
	return w.symlink(name, string(target))
}

func (w *writer) link(name, target string) error {
	if !w.files[target] {
		return fmt.Errorf("%s: hardlink target %q is not an earlier regular file", name, target)
	}
	if err := w.parent(name); err != nil {
		return err
	}
	w.files[name] = true
	return w.root.Link(target, name)
}
