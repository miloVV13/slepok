// Package safepath validates paths and link targets read from archives.
//
// All archive paths are relative, slash-separated and already clean, so a
// path that passes Check can be joined to a destination directory without
// escaping it.
package safepath

import (
	"fmt"
	"path"
	"strings"
)

// Check reports whether p is a safe relative archive path.
func Check(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("%q: absolute path", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q: control character in path", p)
		}
		if r == '\\' {
			return fmt.Errorf("%q: backslash in path", p)
		}
	}
	for _, c := range strings.Split(p, "/") {
		switch c {
		case "":
			return fmt.Errorf("%q: empty path component", p)
		case ".", "..":
			return fmt.Errorf("%q: %q path component", p, c)
		}
	}
	return nil
}

// CheckLinkTarget reports whether a symlink at linkPath pointing to target
// stays inside the archive root. linkPath must already pass Check.
func CheckLinkTarget(linkPath, target string) error {
	if target == "" {
		return fmt.Errorf("%q: empty symlink target", linkPath)
	}
	if strings.HasPrefix(target, "/") {
		return fmt.Errorf("%q: absolute symlink target %q", linkPath, target)
	}
	if strings.ContainsAny(target, "\\\x00") {
		return fmt.Errorf("%q: invalid symlink target %q", linkPath, target)
	}
	resolved := path.Join(path.Dir(linkPath), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%q: symlink target %q escapes root", linkPath, target)
	}
	return nil
}
