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

func TestFlashDryRunESP32AndMonitorNeverExecutePython(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SLEPOK_HOME", filepath.Join(root, "store"))
	st, err := openStore()
	must(t, err)
	t.Cleanup(func() { _ = store.RemoveTree(st.Root()) })
	tree := filepath.Join(root, "python")
	tool := filepath.Join(tree, "bin", "python3")
	if runtime.GOOS == "windows" {
		tool = filepath.Join(tree, "python.exe")
	}
	must(t, os.MkdirAll(filepath.Dir(tool), 0755))
	must(t, os.WriteFile(tool, []byte("not executable Python"), 0755))
	digest, err := st.InstallDir(tree, "")
	must(t, err)
	saved := profile.FS
	t.Cleanup(func() { profile.FS = saved })
	text := "id=\"esp32\"\nversion=\"1\"\nboards=[\"esp32\"]\n"
	for _, name := range []string{"python", "esp-idf-python"} {
		text += "[[components]]\nname=\"" + name + "\"\nversion=\"1\"\n[components.sources.any]\nurl=\"https://example.invalid/python.zip\"\nsha256=\"" + strings.Repeat("a", 64) + "\"\nformat=\"zip\"\n"
	}
	profile.FS = fstest.MapFS{"profiles/esp32.toml": {Data: []byte(text)}}
	dir := filepath.Join(root, "project")
	build := filepath.Join(dir, "build")
	must(t, os.MkdirAll(build, 0755))
	must(t, project.WriteConfig(dir, project.Config{Profile: "esp32", Board: "esp32", Platforms: []string{manifest.HostPlatform()}, BuildDir: "build"}))
	lock := project.Lock{Profile: "esp32", ProfileVersion: "1"}
	for _, name := range []string{"python", "esp-idf-python"} {
		lock.Packages = append(lock.Packages, project.LockedPackage{Name: name, Platform: manifest.HostPlatform(), Source: profile.Source{Digest: digest}})
	}
	must(t, project.WriteLock(dir, lock))
	h := make([]byte, 24)
	h[0] = 0xe9
	for _, name := range []string{"boot.bin", "app.bin", "table.bin"} {
		must(t, os.WriteFile(filepath.Join(build, name), h, 0644))
	}
	must(t, os.WriteFile(filepath.Join(build, "flasher_args.json"), []byte(`{
"write_flash_args":["--flash_mode","dio","--flash_freq","40m","--flash_size","2MB"],
"flash_files":{"0x1000":"boot.bin","0x8000":"table.bin","0x10000":"app.bin"},
"bootloader":{"offset":"0x1000","file":"boot.bin","encrypted":"false"},
"app":{"offset":"0x10000","file":"app.bin","encrypted":"false"},
"partition-table":{"offset":"0x8000","file":"table.bin","encrypted":"false"},
"extra_esptool_args":{"chip":"esp32","before":"default_reset","after":"hard_reset","stub":true}
}`), 0644))
	elf := make([]byte, 52)
	copy(elf, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	binary.LittleEndian.PutUint16(elf[16:], 2)
	binary.LittleEndian.PutUint16(elf[18:], 94)
	binary.LittleEndian.PutUint32(elf[20:], 1)
	binary.LittleEndian.PutUint16(elf[40:], 52)
	must(t, os.WriteFile(filepath.Join(build, "app.elf"), elf, 0644))
	t.Chdir(dir)
	must(t, execute([]string{"flash", "--port", "COM7", "--dry-run"}))
	must(t, execute([]string{"monitor", "--port", "COM7", "--dry-run"}))
	must(t, execute([]string{"ports", "--dry-run"}))
	mustFail(t, execute([]string{"flash", "--dry-run"}), "--port")
	mustFail(t, execute([]string{"flash", "--port", "COM7", "--elf", "build/app.elf", "--dry-run"}), "--elf")
	mustFail(t, execute([]string{"flash", "--port", "COM7", "--probe", "cmsis-dap", "--dry-run"}), "--probe")
	mustFail(t, execute([]string{"monitor", "--port", "COM7", "--baud", "0", "--dry-run"}), "--baud")
}
