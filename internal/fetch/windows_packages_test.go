package fetch

import (
	"debug/pe"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

// Opt-in archive preparation only: no Windows executable is run on Linux.
// The store is retained for inspection; all vendor checksums are verified.
func TestPrepareWindowsPackages(t *testing.T) {
	root := os.Getenv("SLEPOK_WINDOWS_PACKAGES_OUT")
	if root == "" {
		t.Skip("SLEPOK_WINDOWS_PACKAGES_OUT not set")
	}
	st, err := store.Open(root)
	requireOK(t, err)
	guard, err := st.Acquire(t.Context(), false)
	requireOK(t, err)
	defer guard.Close()
	prof, err := profile.Load("rp2040")
	requireOK(t, err)
	f := New(st, os.Stdout)
	f.Context = t.Context()
	required := map[string][]string{
		"arm-gcc": {"bin/arm-none-eabi-gcc.exe", "bin/arm-none-eabi-g++.exe", "bin/arm-none-eabi-objcopy.exe", "libexec/gcc/arm-none-eabi/14.2.1/cc1.exe", "libexec/gcc/arm-none-eabi/14.2.1/cc1plus.exe"},
		"cmake":   {"bin/cmake.exe"}, "ninja": {"ninja.exe"}, "python": {"python.exe", "python312.dll", "vcruntime140.dll", "vcruntime140_1.dll"},
		"pioasm":   {"pioasm/pioasm.exe", "pioasm/pioasmConfig.cmake"},
		"picotool": {"picotool/picotool.exe", "picotool/picotoolConfig.cmake"},
	}
	results := map[string]string{}
	for _, c := range prof.Components {
		if c.Name == "pico-sdk" {
			continue
		} // separately checked against the full SDK tree
		src, _, err := c.Source("windows-x64")
		requireOK(t, err)
		t.Logf("preparing %s %s", c.Name, c.Version)
		digest, err := f.Install(c.Name, src)
		requireOK(t, err)
		dir, err := st.Path(digest)
		requireOK(t, err)
		exported, _, err := pkgtar.Write(io.Discard, dir)
		requireOK(t, err)
		if exported != digest {
			t.Fatalf("%s changed after store extraction: %s != %s", c.Name, exported, digest)
		}
		for _, rel := range required[c.Name] {
			info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
			requireOK(t, err)
			if !info.Mode().IsRegular() {
				t.Fatalf("%s: missing executable/config %s", c.Name, rel)
			}
			if strings.HasSuffix(rel, ".exe") || strings.HasSuffix(rel, ".dll") {
				image, err := pe.Open(filepath.Join(dir, filepath.FromSlash(rel)))
				requireOK(t, err)
				if image.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
					image.Close()
					t.Fatalf("%s/%s is not a Windows AMD64 binary", c.Name, rel)
				}
				symbols, err := image.ImportedSymbols()
				requireOK(t, err)
				libraries := map[string]bool{}
				for _, symbol := range symbols {
					if i := strings.LastIndex(symbol, ":"); i >= 0 {
						libraries[symbol[i+1:]] = true
					}
				}
				var imports []string
				for library := range libraries {
					imports = append(imports, library)
				}
				sort.Strings(imports)
				if len(imports) == 0 {
					image.Close()
					t.Fatalf("%s/%s: no PE imports found", c.Name, rel)
				}
				t.Logf("%s/%s imports: %v", c.Name, rel, imports)
				requireOK(t, image.Close())
			}
		}
		results[c.Name] = digest
		t.Logf("%s = %s", c.Name, digest)
		data, err := json.MarshalIndent(results, "", "  ")
		requireOK(t, err)
		requireOK(t, os.WriteFile(filepath.Join(root, "windows-digests.json"), data, 0o644))
	}
}
