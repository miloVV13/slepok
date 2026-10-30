// Package safepath validates paths and link targets read from archives.
//
// Archive paths use a host-independent, relative, slash-separated syntax.
// Before extraction, CheckForHost also rejects platform-specific aliases,
// devices and alternate streams.
package safepath

import (
	"fmt"
	"path"
	"runtime"
	"strings"
)

// Check reports whether p has safe, host-independent archive path syntax.
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

// CheckForHost additionally checks names written to the host filesystem.
// Archive validation itself stays host-independent through Check.
func CheckForHost(p string) error {
	if err := Check(p); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return checkWindowsNames(p)
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

// CheckLinkTargetForHost also rejects platform-specific link targets when
// extracting. Validate can still inspect a Linux archive on Windows.
func CheckLinkTargetForHost(linkPath, target string) error {
	if err := CheckLinkTarget(linkPath, target); err != nil {
		return err
	}
	resolved := path.Join(path.Dir(linkPath), target)
	if runtime.GOOS == "windows" && resolved != "." {
		return CheckWindows(resolved)
	}
	return nil
}

// CheckWindows applies the Windows-compatible subset of archive paths.
// It is host-independent so the policy can be tested before native Windows
// validation. Linux archives retain their existing naming rules.
func CheckWindows(p string) error {
	if err := Check(p); err != nil {
		return err
	}
	return checkWindowsNames(p)
}

func checkWindowsNames(p string) error {
	for _, component := range strings.Split(p, "/") {
		if strings.ContainsAny(component, `<>:"|?*`) {
			return fmt.Errorf("%q: invalid Windows path character", p)
		}
		if strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
			return fmt.Errorf("%q: Windows path component ends in a dot or space", p)
		}
		base, _, _ := strings.Cut(component, ".")
		base = strings.ToUpper(strings.TrimRight(base, " "))
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$"
		for _, prefix := range []string{"COM", "LPT"} {
			if suffix, ok := strings.CutPrefix(base, prefix); ok {
				reserved = reserved || (len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9') || suffix == "¹" || suffix == "²" || suffix == "³"
			}
		}
		if reserved {
			return fmt.Errorf("%q: reserved Windows device name", p)
		}
	}
	return nil
}
