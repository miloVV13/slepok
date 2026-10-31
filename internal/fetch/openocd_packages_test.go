package fetch

import (
	"debug/macho"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/process"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

// Opt-in preparation verifies vendor checksums and canonical package digests.
// Native runs exercise script parsing and shutdown without opening USB.
func TestRealOpenOCDPackages(t *testing.T) {
	root := os.Getenv("SLEPOK_OPENOCD_PACKAGES")
	if root == "" {
		t.Skip("SLEPOK_OPENOCD_PACKAGES not set")
	}
	st, err := store.Open(root)
	requireOK(t, err)
	guard, err := st.Acquire(t.Context(), false)
	requireOK(t, err)
	defer guard.Close()
	p, err := profile.Load("rp2040")
	requireOK(t, err)
	var c profile.Component
	for _, component := range p.Components {
		if component.Name == "openocd" {
			c = component
		}
	}
	if c.Name == "" {
		t.Fatal("OpenOCD is missing from RP2040")
	}
	f := New(st, os.Stdout)
	f.Context = t.Context()
	for _, platform := range []string{"linux-x64", "windows-x64", "macos-arm64", "macos-x64"} {
		t.Run(platform, func(t *testing.T) {
			src, _, err := c.Source(platform)
			requireOK(t, err)
			digest, err := f.Install(c.Name, src)
			requireOK(t, err)
			t.Logf("%s = %s", platform, digest)
			dir, err := st.Path(digest)
			requireOK(t, err)
			for _, rel := range []string{"openocd/scripts/interface/cmsis-dap.cfg", "openocd/scripts/target/rp2040.cfg", "openocd/contrib/60-openocd.rules"} {
				_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
				requireOK(t, err)
			}
			if platform == "macos-arm64" {
				checkMacOSBinary(t, filepath.Join(dir, "bin", "openocd"), macho.CpuArm64)
			}
			if platform == "macos-x64" {
				checkMacOSBinary(t, filepath.Join(dir, "bin", "openocd"), macho.CpuAmd64)
			}
			if platform != manifest.HostPlatform() {
				return
			}
			binary := filepath.Join(dir, "bin", "openocd")
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			cmd := process.Command(t.Context(), binary, "-s", filepath.Join(dir, "openocd", "scripts"),
				"-c", "gdb port disabled", "-c", "tcl port disabled", "-c", "telnet port disabled",
				"-f", "interface/cmsis-dap.cfg", "-c", "transport select swd", "-f", "target/rp2040.cfg", "-c", "shutdown")
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + filepath.Join(dir, "bin"), "HOME=" + t.TempDir()}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("OpenOCD script smoke: %v\n%s", err, out)
			}
			t.Logf("%s", out)
			// Ensure the complete package remains exportable after execution.
			exported, _, err := pkgtar.Write(io.Discard, dir)
			requireOK(t, err)
			if exported != digest {
				t.Fatal("OpenOCD changed its package during execution")
			}
		})
	}
}
