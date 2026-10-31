package fetch

import (
	"debug/macho"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

// Fetch independent assets concurrently; checksum locks also make this safe
// alongside the inspection test. Nothing is installed or executed here.
func TestDownloadMacOSArchives(t *testing.T) {
	root := os.Getenv("SLEPOK_MACOS_PACKAGES_OUT")
	if root == "" {
		t.Skip("SLEPOK_MACOS_PACKAGES_OUT not set")
	}
	st, err := store.Open(root)
	requireOK(t, err)
	guard, err := st.Acquire(t.Context(), false)
	requireOK(t, err)
	t.Cleanup(func() { _ = guard.Close() })
	p, err := profile.Load("rp2040")
	requireOK(t, err)
	f := New(st, io.Discard)
	f.Context = t.Context()
	seen := map[string]bool{}
	for _, platform := range []string{"macos-arm64", "macos-x64"} {
		for _, c := range p.Components {
			if !c.Supports(platform) {
				continue
			}
			src, _, err := c.Source(platform)
			requireOK(t, err)
			if src.URL == "" || seen[src.SHA256] {
				continue
			}
			seen[src.SHA256] = true
			t.Run(platform+"/"+c.Name, func(t *testing.T) {
				t.Parallel()
				_, err := f.Download(c.Name, src.URL, src.SHA256, src.Mirrors...)
				requireOK(t, err)
			})
		}
	}
}

// Opt-in vendor preparation and static inspection; never runs Mach-O on Linux.
func TestPrepareMacOSPackages(t *testing.T) {
	root := os.Getenv("SLEPOK_MACOS_PACKAGES_OUT")
	if root == "" {
		t.Skip("SLEPOK_MACOS_PACKAGES_OUT not set")
	}
	st, err := store.Open(root)
	requireOK(t, err)
	guard, err := st.Acquire(t.Context(), false)
	requireOK(t, err)
	defer guard.Close()
	p, err := profile.Load("rp2040")
	requireOK(t, err)
	f := New(st, os.Stdout)
	f.Context = t.Context()
	paths := map[string][]string{
		"arm-gcc": {"bin/arm-none-eabi-gcc", "bin/arm-none-eabi-g++", "bin/arm-none-eabi-objcopy", "bin/arm-none-eabi-objdump", "libexec/gcc/arm-none-eabi/14.2.1/cc1", "libexec/gcc/arm-none-eabi/14.2.1/cc1plus"},
		"cmake":   {"CMake.app/Contents/bin/cmake"}, "ninja": {"ninja"}, "python": {"bin/python3"},
		"pioasm": {"pioasm/pioasm"}, "picotool": {"picotool/picotool", "picotool/libusb-1.0.0.dylib"},
		"zstd":    {"lib/libzstd.1.dylib"},
		"openocd": {"bin/openocd"},
	}
	results := map[string]string{}
	for _, platform := range []string{"macos-arm64", "macos-x64"} {
		cpu := macho.CpuArm64
		if platform == "macos-x64" {
			cpu = macho.CpuAmd64
		}
		for _, c := range p.Components {
			if !c.Supports(platform) {
				continue
			}
			if c.Name == "pico-sdk" {
				continue
			} // existing any/Unix digest is retained
			t.Run(platform+"/"+c.Name, func(t *testing.T) {
				src, _, err := c.Source(platform)
				requireOK(t, err)
				t.Logf("preparing %s/%s", platform, c.Name)
				digest, err := f.Install(c.Name, src)
				requireOK(t, err)
				dir, err := st.Path(digest)
				requireOK(t, err)
				exported, _, err := pkgtar.Write(io.Discard, dir)
				requireOK(t, err)
				if exported != digest {
					t.Fatalf("%s changed after extraction", c.Name)
				}
				for _, rel := range paths[c.Name] {
					file := filepath.Join(dir, filepath.FromSlash(rel))
					info, err := os.Stat(file)
					requireOK(t, err)
					if !info.Mode().IsRegular() || (!strings.HasSuffix(rel, ".dylib") && info.Mode().Perm()&0111 == 0) {
						t.Fatalf("%s: not an executable", rel)
					}
					var runtime []string
					if platform == "macos-x64" && c.Name == "arm-gcc" {
						runtime = []string{"/usr/local/opt/zstd/lib/libzstd.1.dylib"}
					}
					checkMacOSBinary(t, file, cpu, runtime...)
				}
				results[platform+"/"+c.Name] = digest
				t.Logf("%s/%s = %s", platform, c.Name, digest)
				data, err := json.MarshalIndent(results, "", "  ")
				requireOK(t, err)
				requireOK(t, os.WriteFile(filepath.Join(root, "macos-digests.json"), data, 0644))
			})
		}
	}
}

func checkMacOSBinary(t *testing.T, file string, cpu macho.Cpu, runtimeLibraries ...string) {
	t.Helper()
	check := func(image *macho.File) {
		libs, err := image.ImportedLibraries()
		requireOK(t, err)
		for _, lib := range libs {
			allowedRuntime := false
			for _, runtime := range runtimeLibraries {
				if lib == runtime {
					allowedRuntime = true
				}
			}
			if allowedRuntime {
				continue
			}
			if strings.HasPrefix(lib, "/") && !strings.HasPrefix(lib, "/usr/lib/") && !strings.HasPrefix(lib, "/System/Library/") {
				t.Fatalf("%s: external library %s", file, lib)
			}
		}
		t.Logf("%s %s libraries: %v", filepath.Base(file), cpu, libs)
	}
	if fat, err := macho.OpenFat(file); err == nil {
		defer fat.Close()
		for _, arch := range fat.Arches {
			if arch.Cpu == cpu {
				check(arch.File)
				return
			}
		}
		t.Fatalf("%s: missing %s architecture", file, cpu)
	}
	image, err := macho.Open(file)
	requireOK(t, err)
	defer image.Close()
	if image.Cpu != cpu {
		t.Fatalf("%s: CPU %s, want %s", file, image.Cpu, cpu)
	}
	check(image)
}
