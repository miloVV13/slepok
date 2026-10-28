// Package manifest defines manifest.json, the first entry of every
// snapshot (spec 9.3).
package manifest

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/miloVV13/slepok/internal/safepath"
)

// FormatVersion is the newest snapshot format this build reads and the one
// it writes.
const FormatVersion = 1

// MaxSize bounds the manifest entry so a hostile archive cannot make the
// reader allocate unbounded memory.
const MaxSize = 64 << 20

// EntryName is the archive path of the manifest.
const EntryName = "manifest.json"

// AnyPlatform marks a package that works on every host platform, such as
// an SDK distributed as source.
const AnyPlatform = "any"

type Manifest struct {
	FormatVersion int       `json:"formatVersion"`
	CreatedBy     string    `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
	Profile       Profile   `json:"profile"`
	Platforms     []string  `json:"platforms"`
	Packages      []Package `json:"packages"`
	Files         []File    `json:"files"`
}

type Profile struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Package is one environment component stored as packages/<hex>.tar.
type Package struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	License  string `json:"license,omitempty"`
}

// File is a project or report file stored under its archive path.
type File struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
	Executable bool   `json:"executable,omitempty"`
}

// Entry returns the archive path of the package.
func (p Package) Entry() string {
	return "packages/" + strings.TrimPrefix(p.Digest, "sha256:") + ".tar"
}

// TooNewError reports a snapshot written by a newer, incompatible format.
type TooNewError struct{ Version int }

func (e *TooNewError) Error() string {
	return fmt.Sprintf("snapshot format version %d is newer than the supported version %d; update slepok", e.Version, FormatVersion)
}

// Parse decodes and validates a manifest. The format version is checked
// before anything else so newer snapshots fail with a clear message.
func Parse(data []byte) (*Manifest, error) {
	var head struct {
		FormatVersion *int `json:"formatVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON: %w", err)
	}
	switch v := head.FormatVersion; {
	case v == nil:
		return nil, fmt.Errorf("manifest has no formatVersion")
	case *v > FormatVersion:
		return nil, &TooNewError{*v}
	case *v < 1:
		return nil, fmt.Errorf("invalid manifest formatVersion %d", *v)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Marshal encodes the manifest after validating it.
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}

var (
	platformRe = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	digestRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Validate checks the manifest's internal consistency.
func (m *Manifest) Validate() error {
	if len(m.Platforms) == 0 {
		return fmt.Errorf("manifest lists no platforms")
	}
	platforms := map[string]bool{}
	for _, p := range m.Platforms {
		if !platformRe.MatchString(p) {
			return fmt.Errorf("invalid platform %q", p)
		}
		platforms[p] = true
	}

	entries := map[string]bool{EntryName: true}
	for _, p := range m.Packages {
		switch {
		case p.Name == "":
			return fmt.Errorf("package with empty name")
		case !digestRe.MatchString(p.Digest):
			return fmt.Errorf("package %s: invalid digest %q", p.Name, p.Digest)
		case !platforms[p.Platform] && p.Platform != AnyPlatform:
			return fmt.Errorf("package %s: platform %q not listed in platforms", p.Name, p.Platform)
		case p.Size < 0:
			return fmt.Errorf("package %s: negative size", p.Name)
		case entries[p.Entry()]:
			return fmt.Errorf("package %s: duplicate digest %s", p.Name, p.Digest)
		}
		entries[p.Entry()] = true
	}

	files := map[string]bool{}
	for _, f := range m.Files {
		if err := safepath.Check(f.Path); err != nil {
			return fmt.Errorf("file: %w", err)
		}
		if !strings.HasPrefix(f.Path, "project/") && !strings.HasPrefix(f.Path, "reports/") {
			return fmt.Errorf("file %s: must be under project/ or reports/", f.Path)
		}
		switch {
		case !digestRe.MatchString(f.Digest):
			return fmt.Errorf("file %s: invalid digest %q", f.Path, f.Digest)
		case f.Size < 0:
			return fmt.Errorf("file %s: negative size", f.Path)
		case entries[f.Path]:
			return fmt.Errorf("file %s: duplicate entry", f.Path)
		}
		entries[f.Path] = true
		files[f.Path] = true
	}
	// A path cannot be both a file and a directory of another file.
	for p := range files {
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			if files[d] {
				return fmt.Errorf("file %s: parent %s is a file", p, d)
			}
		}
	}
	return nil
}

// HostPlatform returns the platform name of the running machine, e.g.
// "linux-x64".
func HostPlatform() string {
	os := runtime.GOOS
	if os == "darwin" {
		os = "macos"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return os + "-" + arch
}
