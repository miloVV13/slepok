package runner

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/process"
)

func flashFixture(t *testing.T) (*Env, string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project with spaces $x [x] {x};")
	root := filepath.Join(t.TempDir(), "openocd")
	for _, rel := range []string{"bin", "openocd/scripts/interface", "openocd/scripts/target"} {
		must(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0755))
	}
	tool := "openocd"
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	must(t, os.WriteFile(filepath.Join(root, "bin", tool), []byte("fixture"), 0755))
	for _, rel := range []string{"interface/cmsis-dap.cfg", "target/rp2040.cfg"} {
		must(t, os.WriteFile(filepath.Join(root, "openocd", "scripts", filepath.FromSlash(rel)), nil, 0644))
	}
	build := filepath.Join(dir, "build")
	must(t, os.MkdirAll(build, 0755))
	// Minimal ELF32 ARM executable header; no target is accessed by these tests.
	header := make([]byte, 52)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	binary.LittleEndian.PutUint16(header[16:], 2)
	binary.LittleEndian.PutUint16(header[18:], 40)
	binary.LittleEndian.PutUint32(header[20:], 1)
	binary.LittleEndian.PutUint16(header[40:], 52)
	must(t, os.WriteFile(filepath.Join(build, "blink.elf"), header, 0644))
	return &Env{Packages: map[string]string{"openocd": root}}, dir, build
}

func TestFlashPlanAndErrors(t *testing.T) {
	e, dir, build := flashFixture(t)
	p, err := PlanRP2040Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Serial: `probe $x [x] "`, Speed: 1000})
	must(t, err)
	if p.Firmware != filepath.Join(build, "blink.elf") || !strings.Contains(p.Args[len(p.Args)-1], `verify reset exit`) {
		t.Fatalf("wrong plan: %+v", p)
	}
	if !strings.Contains(p.Args[len(p.Args)-1], `\$x \[x\]`) {
		t.Fatalf("Tcl substitutions not quoted: %v", p.Args)
	}
	if slices.Contains(p.Args, "cmsis-dap quirk enable") {
		t.Fatal("compatibility mode enabled by default")
	}
	compatible, err := PlanRP2040Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 5000, CMSISDAPQuirk: true})
	must(t, err)
	index := slices.Index(compatible.Args, "cmsis-dap quirk enable")
	if index < 1 || compatible.Args[index-1] != "-c" || index >= slices.Index(compatible.Args, filepath.Join(e.Packages["openocd"], "openocd", "scripts", "target", "rp2040.cfg")) {
		t.Fatal("compatibility mode must be enabled before target initialization")
	}
	for _, tc := range []struct {
		name, file, probe string
		speed             int
	}{
		{"probe", "", "stlink", 1000}, {"speed", "", "cmsis-dap", 0},
		{"missing", "missing.elf", "cmsis-dap", 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanRP2040Flash(e, dir, build, FlashOptions{Firmware: tc.file, Probe: tc.probe, Speed: tc.speed}); err == nil {
				t.Fatal("invalid flash request accepted")
			}
		})
	}
	must(t, os.WriteFile(filepath.Join(build, "other.elf"), []byte("not an ELF"), 0644))
	if _, err := PlanRP2040Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 1000}); err == nil || !strings.Contains(err.Error(), "--elf") {
		t.Fatalf("ambiguous firmware accepted: %v", err)
	}
	if _, err := PlanRP2040Flash(e, dir, build, FlashOptions{Firmware: "build/other.elf", Probe: "cmsis-dap", Speed: 1000}); err == nil {
		t.Fatal("invalid ELF accepted")
	}
	_, err = PlanRP2040Flash(e, dir, build, FlashOptions{Firmware: "build/blink.elf", Probe: "cmsis-dap", Speed: 1000})
	must(t, err)
	delete(e.Packages, "openocd")
	if _, err := PlanRP2040Flash(e, dir, build, FlashOptions{Firmware: "build/blink.elf", Probe: "cmsis-dap", Speed: 1000}); err == nil || !strings.Contains(err.Error(), "slepok init") {
		t.Fatalf("missing locked tool accepted: %v", err)
	}
}

// Uses the real Tcl parser, replacing program before it can initialize USB.
func TestRealOpenOCDFlashArguments(t *testing.T) {
	root := os.Getenv("SLEPOK_OPENOCD_ROOT")
	if root == "" {
		t.Skip("SLEPOK_OPENOCD_ROOT not set")
	}
	e, dir, build := flashFixture(t)
	e.Packages["openocd"] = root
	p, err := PlanRP2040Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Serial: `serial $x [x] "`, Speed: 1000, CMSISDAPQuirk: true})
	must(t, err)
	args := append([]string(nil), p.Args[:len(p.Args)-2]...)
	args = append(args, "-c", `proc program {file args} {echo "SLEPOK_FIRMWARE=$file"; shutdown}`)
	args = append(args, p.Args[len(p.Args)-2:]...)
	cmd := process.Command(t.Context(), p.Executable, args...)
	cmd.Dir = p.Directory
	cmd.Env = e.buildEnviron()
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("SLEPOK_FIRMWARE="+filepath.ToSlash(p.Firmware))) {
		t.Fatalf("literal path round trip: %v\n%s", err, out)
	}
}

func TestFlashProcessHelper(t *testing.T) {
	if os.Getenv("SLEPOK_FLASH_TEST_CHILD") != "1" {
		return
	}
	if os.Getenv("OPENOCD_SCRIPTS") != "" || os.Getenv("SLEPOK_HOST_MARKER") != "" {
		os.Exit(9)
	}
	fmt.Fprintln(os.Stderr, "controlled environment reached")
	os.Exit(7)
}

func TestFlashExecutionFailureAndCancellation(t *testing.T) {
	t.Setenv("OPENOCD_SCRIPTS", "host scripts")
	t.Setenv("SLEPOK_HOST_MARKER", "host value")
	executable, err := os.Executable()
	must(t, err)
	p := &FlashPlan{Executable: executable, Directory: t.TempDir(), Args: []string{"-test.run=^TestFlashProcessHelper$"}}
	e := &Env{Vars: map[string]string{"SLEPOK_FLASH_TEST_CHILD": "1"}}
	var log bytes.Buffer
	err = Flash(t.Context(), e, p, &log, &log)
	if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "USB permissions/drivers") || !strings.Contains(log.String(), "controlled environment reached") {
		t.Fatalf("lost process failure or inherited host environment: %v\n%s", err, &log)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Flash(ctx, e, p, &log, &log); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
