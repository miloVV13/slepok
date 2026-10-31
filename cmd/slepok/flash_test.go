package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

func TestFlashDryRunDoesNotBuildOrExecuteTool(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SLEPOK_HOME", filepath.Join(root, "store"))
	st, err := openStore()
	must(t, err)
	t.Cleanup(func() { _ = store.RemoveTree(st.Root()) })
	tree := filepath.Join(root, "tool")
	for _, dir := range []string{"bin", "openocd/scripts/interface", "openocd/scripts/target"} {
		must(t, os.MkdirAll(filepath.Join(tree, filepath.FromSlash(dir)), 0755))
	}
	tool := "openocd"
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	// This deliberately cannot execute. There is no cmake in the environment.
	must(t, os.WriteFile(filepath.Join(tree, "bin", tool), []byte("not executable code"), 0755))
	for _, file := range []string{"interface/cmsis-dap.cfg", "target/rp2040.cfg"} {
		must(t, os.WriteFile(filepath.Join(tree, "openocd/scripts", filepath.FromSlash(file)), nil, 0644))
	}
	digest, err := st.InstallDir(tree, "")
	must(t, err)
	saved := profile.FS
	t.Cleanup(func() { profile.FS = saved })
	profile.FS = fstest.MapFS{"profiles/rp2040.toml": {Data: []byte(`id = "rp2040"
version = "1"
boards = ["pico"]
[[components]]
name = "openocd"
version = "1"
build_optional = true
[components.sources.any]
url = "https://example.invalid/openocd.zip"
sha256 = "` + strings.Repeat("a", 64) + `"
format = "zip"
`)}}
	dir := filepath.Join(root, "project")
	must(t, os.MkdirAll(filepath.Join(dir, "build"), 0755))
	must(t, project.WriteConfig(dir, project.Config{Profile: "rp2040", Board: "pico", Platforms: []string{manifest.HostPlatform()}, BuildDir: "build"}))
	lock := project.Lock{Profile: "rp2040", ProfileVersion: "1", Packages: []project.LockedPackage{{Name: "openocd", Version: "1", Platform: manifest.HostPlatform(), Source: profile.Source{Digest: digest}}}}
	must(t, project.WriteLock(dir, lock))
	header := make([]byte, 52)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	binary.LittleEndian.PutUint16(header[16:], 2)
	binary.LittleEndian.PutUint16(header[18:], 40)
	binary.LittleEndian.PutUint32(header[20:], 1)
	binary.LittleEndian.PutUint16(header[40:], 52)
	must(t, os.WriteFile(filepath.Join(dir, "build", "blink.elf"), header, 0644))
	t.Chdir(dir)
	must(t, execute([]string{"flash", "--dry-run"}))
	must(t, execute([]string{"flash", "--dry-run", "--cmsis-dap-quirk"}))
	mustFail(t, execute([]string{"run"}), "requires --ram")
	mustFail(t, execute([]string{"run", "--ram", "--dry-run", "--elf", "build/blink.elf"}), "RAM image")
	mustFail(t, execute([]string{"flash", "--dry-run", "--probe", "unknown"}), "cmsis-dap")
	mustFail(t, execute([]string{"flash", "--dry-run", "--speed", "0"}), "speed")
	lock.Packages = nil
	must(t, project.WriteLock(dir, lock))
	mustFail(t, execute([]string{"flash", "--dry-run"}), "slepok init")
}
