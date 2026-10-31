package runner

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/process"
)

func stm32ELF() []byte {
	data := ramELF()
	for _, off := range []int{24, 60, 64} {
		value := uint32(0x08000000)
		if off == 24 {
			value++
		}
		binary.LittleEndian.PutUint32(data[off:], value)
	}
	return data
}

func TestSTM32FlashChecksMemoryAndTarget(t *testing.T) {
	e, dir, build := flashFixture(t)
	must(t, os.WriteFile(filepath.Join(e.Packages["openocd"], "openocd/scripts/target/stm32f1x.cfg"), nil, 0644))
	file := filepath.Join(build, "blink.elf")
	must(t, os.WriteFile(file, stm32ELF(), 0644))
	p, err := PlanSTM32F103Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 1000})
	must(t, err)
	if !strings.Contains(p.String(), "stm32f1x.cfg") || strings.Contains(p.String(), "rp2040.cfg") || !strings.Contains(p.Args[len(p.Args)-1], "verify reset exit") {
		t.Fatalf("wrong target command: %s", p)
	}
	for _, tc := range []struct {
		off   int
		value uint32
	}{
		{24, 0x10000001}, {64, 0x10000000}, {68, 0x10001}, {60, 0x20005000}, {72, 0x10001},
	} {
		data := stm32ELF()
		binary.LittleEndian.PutUint32(data[tc.off:], tc.value)
		must(t, os.WriteFile(file, data, 0644))
		if _, err := PlanSTM32F103Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 1000}); err == nil {
			t.Fatal("wrong target or oversized image accepted")
		}
	}
}

func TestRealSTM32OpenOCDPlan(t *testing.T) {
	root := os.Getenv("SLEPOK_OPENOCD_ROOT")
	if root == "" {
		t.Skip("SLEPOK_OPENOCD_ROOT not set")
	}
	e, dir, build := flashFixture(t)
	e.Packages["openocd"] = root
	must(t, os.WriteFile(filepath.Join(build, "blink.elf"), stm32ELF(), 0644))
	p, err := PlanSTM32F103Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 1000, CMSISDAPQuirk: true})
	must(t, err)
	args := slices.Clone(p.Args[:len(p.Args)-2])
	args = append(args, "-c", `proc program {file args} {echo "SLEPOK_STM32_FILE=$file"; shutdown}`)
	args = append(args, p.Args[len(p.Args)-2:]...)
	cmd := process.Command(t.Context(), p.Executable, args...)
	cmd.Dir, cmd.Env = p.Directory, e.buildEnviron()
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("SLEPOK_STM32_FILE="+filepath.ToSlash(p.Firmware))) {
		t.Fatalf("STM32 Tcl parsing failed: %v\n%s", err, out)
	}
}
