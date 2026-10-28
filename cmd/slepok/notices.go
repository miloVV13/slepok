package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/miloVV13/slepok/internal/fetch"
	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/snapshot"
	"github.com/miloVV13/slepok/internal/store"
)

// noticeEntry is the human-readable license summary in every snapshot
// (spec 15.1).
const noticeEntry = "reports/NOTICE.txt"

// sourceExtras downloads the published source code of the packages that
// have it, for bundling under sources/.
func sourceExtras(st *store.Store, prof *profile.Profile, packages []snapshot.PackageSource) ([]snapshot.Extra, error) {
	f := fetch.New(st, os.Stdout)
	seen := map[string]bool{}
	var extras []snapshot.Extra
	for _, pkg := range packages {
		if pkg.SourceCode == "" || seen[pkg.SourceCode] {
			continue
		}
		seen[pkg.SourceCode] = true
		var sc *profile.SourceArchive
		for _, c := range prof.Components {
			if c.Name == pkg.Name && c.SourceCode != nil && c.SourceCode.URL == pkg.SourceCode {
				sc = c.SourceCode
			}
		}
		if sc == nil {
			return nil, fmt.Errorf("no checksum for the source code of %s", pkg.Name)
		}
		fmt.Printf("Bundling the source code of %s %s\n", pkg.Name, pkg.Version)
		file, err := f.Download(pkg.Name+" source code", sc.URL, sc.SHA256, sc.Mirrors...)
		if err != nil {
			return nil, fmt.Errorf("source code of %s: %w", pkg.Name, err)
		}
		extras = append(extras, snapshot.Extra{Name: "sources/" + archiveName(sc.URL), Path: file})
	}
	return extras, nil
}

func archiveName(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return path.Base(u.Path)
	}
	return path.Base(raw)
}

// noticeText summarises the third-party software in a snapshot.
func noticeText(packages []snapshot.PackageSource, sources []snapshot.Extra) []byte {
	bundled := map[string]string{}
	for _, s := range sources {
		bundled[strings.TrimPrefix(s.Name, "sources/")] = s.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This snapshot was made with slepok %s (https://github.com/miloVV13/slepok).\n\n", version)
	b.WriteString("It contains third-party software under its own licenses. Every package\n")
	b.WriteString("keeps the license files of its components; they are not modified.\n\n")
	for _, p := range packages {
		fmt.Fprintf(&b, "%s %s (%s)\n  License: %s\n", p.Name, p.Version, p.Platform, p.License)
		if p.SourceCode != "" {
			fmt.Fprintf(&b, "  Source code: %s\n", p.SourceCode)
			if name, ok := bundled[archiveName(p.SourceCode)]; ok {
				fmt.Fprintf(&b, "  Included in this snapshot as %s\n", name)
			}
		}
		if p.Notice != "" {
			fmt.Fprintf(&b, "  Note: %s\n", p.Notice)
		}
		b.WriteString("\n")
	}
	if len(sources) > 0 {
		b.WriteString("The bundled source code can be extracted with:\n  tar --zstd -xf <snapshot>.slepok sources/\n")
	} else {
		b.WriteString("Whoever passes this snapshot on must offer the source code of the GPL-\n")
		b.WriteString("licensed packages, published at the URLs above. 'slepok pack -with-sources'\n")
		b.WriteString("bundles it into the snapshot.\n")
	}
	return []byte(b.String())
}

// printNotices tells the user about license conditions that matter when a
// snapshot is shared.
func printNotices(w io.Writer, m *manifest.Manifest, bundled bool) {
	var gpl []string
	for _, p := range m.Packages {
		if p.Notice != "" {
			fmt.Fprintf(w, "note: %s: %s\n", p.Name, p.Notice)
		}
		if p.SourceCode != "" {
			gpl = append(gpl, p.Name)
		}
	}
	if len(gpl) > 0 && !bundled {
		fmt.Fprintf(w, "note: %s: whoever shares this snapshot must offer the source code (see %s, or pack with -with-sources)\n",
			strings.Join(gpl, ", "), noticeEntry)
	}
}

func hasSources(m *manifest.Manifest) bool {
	for _, f := range m.Files {
		if strings.HasPrefix(f.Path, "sources/") {
			return true
		}
	}
	return false
}
