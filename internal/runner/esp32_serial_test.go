package runner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

// Opt-in real pinned Python, IDF monitor and addr2line, on virtual UART/TTY.
// The harness simulates only modem-line ioctls unsupported by PTYs.
func TestRealESP32MonitorPTY(t *testing.T) {
	dir := os.Getenv("SLEPOK_ESP32_PROJECT")
	if dir == "" {
		t.Skip("SLEPOK_ESP32_PROJECT not set")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTYs")
	}
	p, err := project.Load(dir)
	must(t, err)
	prof, err := profile.Load("esp32")
	must(t, err)
	root, err := store.DefaultRoot()
	must(t, err)
	st, err := store.Open(root)
	must(t, err)
	guard, err := st.Acquire(t.Context(), false)
	must(t, err)
	defer guard.Close()
	e, err := ResolveContext(t.Context(), prof, p.Lock, st, manifest.HostPlatform(), p.Config.Board)
	must(t, err)
	plan, err := PlanESP32Monitor(t.Context(), e, st, p.Dir, p.BuildDir(), "", "/dev/slepok-virtual", 115200, false, false)
	must(t, err)
	data, err := json.Marshal(struct {
		*SerialPlan
		Environment []string
	}{plan, e.buildEnviron()})
	must(t, err)
	file := filepath.Join(t.TempDir(), "monitor-test.json")
	must(t, os.WriteFile(file, data, 0600))
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "esp32_monitor_smoke.py"))
	must(t, err)
	cmd := exec.CommandContext(t.Context(), plan.Executable, script, file)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PTY monitor: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func serialFixture(t *testing.T) (*Env, string, map[string]any) {
	t.Helper()
	root := t.TempDir()
	python := filepath.Join(root, "python")
	tool := filepath.Join(python, "bin", "python3")
	if runtime.GOOS == "windows" {
		tool = filepath.Join(python, "python.exe")
	}
	must(t, os.MkdirAll(filepath.Dir(tool), 0755))
	must(t, os.WriteFile(tool, []byte("must not execute"), 0755))
	build := filepath.Join(root, "project with spaces", "build")
	must(t, os.MkdirAll(filepath.Join(build, "bootloader"), 0755))
	must(t, os.MkdirAll(filepath.Join(build, "partition_table"), 0755))
	header := make([]byte, 24)
	header[0] = 0xe9
	for _, name := range []string{"app.bin", "bootloader/bootloader.bin", "partition_table/partition-table.bin"} {
		must(t, os.WriteFile(filepath.Join(build, filepath.FromSlash(name)), header, 0644))
	}
	meta := map[string]any{
		"write_flash_args":   []string{"--flash_mode", "dio", "--flash_freq", "40m", "--flash_size", "2MB"},
		"flash_files":        map[string]string{"0x10000": "app.bin", "0x1000": "bootloader/bootloader.bin", "0x8000": "partition_table/partition-table.bin"},
		"bootloader":         map[string]any{"offset": "0x1000", "file": "bootloader/bootloader.bin", "encrypted": "false"},
		"app":                map[string]any{"offset": "0x10000", "file": "app.bin", "encrypted": "false"},
		"partition-table":    map[string]any{"offset": "0x8000", "file": "partition_table/partition-table.bin", "encrypted": "false"},
		"extra_esptool_args": map[string]any{"chip": "esp32", "before": "default_reset", "after": "hard_reset", "stub": true},
	}
	return &Env{Packages: map[string]string{"python": python, "esp-idf-python": filepath.Join(root, "libraries")}}, build, meta
}

func writeFlashMetadata(t *testing.T, build string, meta map[string]any) {
	t.Helper()
	data, err := json.Marshal(meta)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(build, "flasher_args.json"), data, 0644))
}

func TestESP32SerialFlashPlanUsesAllMetadataImages(t *testing.T) {
	e, build, meta := serialFixture(t)
	writeFlashMetadata(t, build, meta)
	p, err := PlanESP32Flash(e, build, "COM3", 460800)
	must(t, err)
	if len(p.Files) != 3 || p.Files[0].Offset != 0x1000 || p.Files[1].Offset != 0x8000 || p.Files[2].Offset != 0x10000 {
		t.Fatal(p.Files)
	}
	args := strings.Join(p.Args, "|")
	if !strings.Contains(args, "-m|esptool|--chip|esp32|--port|COM3|--baud|460800") || !strings.Contains(args, "--after|hard_reset|write_flash") {
		t.Fatal(args)
	}
	if strings.Contains(args, "--force") || strings.Contains(args, "erase_flash") {
		t.Fatal("unexpected destructive extra operation", args)
	}
	if p.Directory != e.Packages["python"] {
		t.Fatal("project can shadow Python modules", p.Directory)
	}
	for _, port := range []string{"", "--evil", "socket://host:1234", "rfc2217://host:1234", "COM3\n--force", "COM3\x00"} {
		if _, err := PlanESP32Flash(e, build, port, 460800); err == nil {
			t.Fatal("invalid port accepted", port)
		}
	}
}

func TestESP32FlashMetadataRejectsInvalidImagesBeforePortAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any, string)
	}{
		{"wrong-chip", func(m map[string]any, _ string) { m["extra_esptool_args"].(map[string]any)["chip"] = "esp32s3" }},
		{"encryption", func(m map[string]any, _ string) { m["app"].(map[string]any)["encrypted"] = "true" }},
		{"extra-encryption", func(m map[string]any, _ string) { m["nvs"] = map[string]any{"encrypted": true} }},
		{"arbitrary-flags", func(m map[string]any, _ string) {
			m["write_flash_args"] = []string{"--force", "--erase-all", "--flash_size", "2MB", "--flash_freq", "40m"}
		}},
		{"traversal", func(m map[string]any, _ string) { m["flash_files"].(map[string]string)["0x10000"] = "../outside.bin" }},
		{"missing-file", func(m map[string]any, _ string) { m["flash_files"].(map[string]string)["0x10000"] = "missing.bin" }},
		{"erase-overlap", func(m map[string]any, _ string) { m["flash_files"].(map[string]string)["4096"] = "app.bin" }},
		{"overflow", func(m map[string]any, _ string) { m["flash_files"].(map[string]string)["0x200000"] = "app.bin" }},
		{"bad-offset", func(m map[string]any, _ string) { m["flash_files"].(map[string]string)["0x1001"] = "app.bin" }},
		{"inconsistent-entry", func(m map[string]any, _ string) { m["app"].(map[string]any)["file"] = "bootloader/bootloader.bin" }},
		{"wrong-image-chip", func(_ map[string]any, b string) {
			h := make([]byte, 24)
			h[0] = 0xe9
			binary.LittleEndian.PutUint16(h[12:], 9)
			must(t, os.WriteFile(filepath.Join(b, "app.bin"), h, 0644))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, b, m := serialFixture(t)
			tc.change(m, b)
			writeFlashMetadata(t, b, m)
			if _, err := PlanESP32Flash(e, b, "COM3", 115200); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

func TestESP32SerialMonitorAndPortsPlans(t *testing.T) {
	e, build, _ := serialFixture(t)
	root := t.TempDir()
	st, err := store.Open(root)
	must(t, err)
	t.Cleanup(func() { _ = store.RemoveTree(root) })
	h := make([]byte, 52)
	copy(h, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	binary.LittleEndian.PutUint16(h[16:], 2)
	binary.LittleEndian.PutUint16(h[18:], 94)
	binary.LittleEndian.PutUint32(h[20:], 1)
	binary.LittleEndian.PutUint16(h[40:], 52)
	must(t, os.WriteFile(filepath.Join(build, "app.elf"), h, 0644))
	p, err := PlanESP32Monitor(context.Background(), e, st, filepath.Dir(build), build, "", "COM7", 115200, false, true)
	must(t, err)
	args := strings.Join(p.Args, "|")
	if !strings.Contains(args, "--no-reset|--timestamps") || !strings.HasSuffix(args, "app.elf") {
		t.Fatal(args)
	}
	bridge, err := os.ReadFile(p.Args[0])
	must(t, err)
	if !strings.Contains(string(bridge), "def run_make") || !strings.Contains(string(bridge), "shortcuts are disabled") {
		t.Fatal("build shortcut not adapted")
	}
	ports, err := PlanESP32Ports(e)
	must(t, err)
	if strings.Join(ports.Args, "|") != "-m|serial.tools.list_ports|-v" {
		t.Fatal(ports.Args)
	}
	h[18] = 40
	must(t, os.WriteFile(filepath.Join(build, "app.elf"), h, 0644))
	if _, err := PlanESP32Monitor(context.Background(), e, st, filepath.Dir(build), build, "", "COM7", 115200, true, false); err == nil {
		t.Fatal("ARM image accepted by ESP32 monitor")
	}
}
