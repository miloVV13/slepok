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

// A real, minimal ELF32 with a PT_LOAD, Pico startup symbols and a vector table.
func ramELF() []byte {
	data := make([]byte, 0x400)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	u16 := func(off int, n uint16) { binary.LittleEndian.PutUint16(data[off:], n) }
	u32 := func(off int, n uint32) { binary.LittleEndian.PutUint32(data[off:], n) }
	u16(16, 2)
	u16(18, 40)
	u32(20, 1)
	u32(24, 0x20000001) // entry
	u32(28, 52)         // program headers
	u32(32, 0x380)      // section headers
	u16(40, 52)
	u16(42, 32)
	u16(44, 1)
	u16(46, 40)
	u16(48, 3)
	for i, n := range []uint32{1, 0x100, 0x20000000, 0x20000000, 0x200, 0x1000, 7, 4} {
		u32(52+i*4, n)
	}
	u32(0x200, 0x20042000) // MSP at __vectors
	u32(0x204, 0x20000011) // reset handler
	copy(data[0x330:], "\x00_entry_point\x00__vectors\x00")
	u32(0x310, 1)
	u32(0x314, 0x20000001)
	data[0x31c] = 0x12 // global function
	u16(0x31e, 1)
	u32(0x320, 14)
	u32(0x324, 0x20000100)
	data[0x32c] = 0x10
	u16(0x32e, 1)
	// SHT_SYMTAB linked to the SHT_STRTAB (section 2).
	u32(0x3ac, 2)
	u32(0x3b8, 0x300)
	u32(0x3bc, 48)
	u32(0x3c0, 2)
	u32(0x3cc, 16)
	u32(0x3d4, 3)
	u32(0x3e0, 0x330)
	u32(0x3e4, 24)
	return data
}

func TestRAMImageValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.elf")
	check := func(data []byte) (*RAMImage, error) {
		must(t, os.WriteFile(file, data, 0644))
		return InspectRP2040RAM(file)
	}
	image, err := check(ramELF())
	must(t, err)
	if image.Entry != 0x20000001 || image.Vectors != 0x20000100 || image.Stack != 0x20042000 {
		t.Fatalf("wrong startup metadata: %+v", image)
	}
	for _, tc := range []struct {
		name  string
		off   int
		value uint32
	}{
		{"flash-load-address", 64, 0x10000000},
		{"flash-run-address", 60, 0x10000000},
		{"segment-overflow", 72, 0xffffffff},
		{"file-larger-than-memory", 68, 0x2000},
		{"flash-entry", 24, 0x10000001},
		{"non-thumb-entry", 24, 0x20000000},
		{"entry-in-bss", 24, 0x20000301},
		{"wrong-sdk-entry", 0x314, 0x20000011},
		{"unaligned-vectors", 0x324, 0x20000104},
		{"stack-outside-sram", 0x200, 0x20042008},
		{"unaligned-stack", 0x200, 0x20041fff},
		{"reset-in-flash", 0x204, 0x10000001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := ramELF()
			binary.LittleEndian.PutUint32(data[tc.off:], tc.value)
			if _, err := check(data); err == nil {
				t.Fatal("invalid RAM image accepted")
			}
		})
	}
	for _, flash := range []bool{false, true} {
		data := ramELF()
		binary.LittleEndian.PutUint16(data[44:], 2)
		copy(data[84:116], data[52:84])
		if flash {
			binary.LittleEndian.PutUint32(data[92:], 0x10000000)
			binary.LittleEndian.PutUint32(data[96:], 0x10000000)
		}
		if _, err := check(data); err == nil {
			t.Fatal("overlapping or mixed Flash/RAM image accepted")
		}
	}
	data := ramELF()
	binary.LittleEndian.PutUint16(data[48:], 0)
	binary.LittleEndian.PutUint32(data[32:], 0)
	if _, err := check(data); err == nil || !strings.Contains(err.Error(), "unstripped") {
		t.Fatalf("missing startup symbols accepted: %v", err)
	}
}

func TestRAMPlanNeverProgramsFlashAndStartsCore0(t *testing.T) {
	e, dir, build := flashFixture(t)
	must(t, os.WriteFile(filepath.Join(build, "blink.elf"), ramELF(), 0644))
	if _, err := PlanRP2040Flash(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 5000}); err == nil || !strings.Contains(err.Error(), "run --ram") {
		t.Fatalf("RAM ELF accepted by flash command: %v", err)
	}
	p, err := PlanRP2040RAM(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 5000, CMSISDAPQuirk: true})
	must(t, err)
	for _, command := range p.Args {
		if strings.HasPrefix(command, "program ") || strings.HasPrefix(command, "flash ") || command == "reset init" || command == "reset run" {
			t.Fatalf("RAM plan contains a Flash programming or post-load reset command: %s", command)
		}
	}
	for _, command := range []string{"set USE_CORE 0", "cmsis-dap quirk enable", "reset halt", "reg msp 0x20042000", "mww 0xe000ed08 0x20000100", "resume 0x20000000", "shutdown"} {
		if !slices.Contains(p.Args, command) {
			t.Fatalf("missing RAM startup command: %s", command)
		}
	}
	var load, verify, resume int
	for i, command := range p.Args {
		if strings.HasPrefix(command, "load_image ") {
			load = i
		}
		if strings.HasPrefix(command, "verify_image ") {
			verify = i
		}
		if strings.HasPrefix(command, "resume ") {
			resume = i
		}
	}
	if load == 0 || verify <= load || resume <= verify {
		t.Fatal("RAM image must be loaded and verified before it runs")
	}
}

// Parse the whole plan in native OpenOCD while replacing all hardware commands.
func TestRealOpenOCDRAMPlan(t *testing.T) {
	root := os.Getenv("SLEPOK_OPENOCD_ROOT")
	if root == "" {
		t.Skip("SLEPOK_OPENOCD_ROOT not set")
	}
	e, dir, build := flashFixture(t)
	e.Packages["openocd"] = root
	must(t, os.WriteFile(filepath.Join(build, "blink.elf"), ramELF(), 0644))
	p, err := PlanRP2040RAM(e, dir, build, FlashOptions{Probe: "cmsis-dap", Speed: 5000, CMSISDAPQuirk: true})
	must(t, err)
	index := slices.Index(p.Args, "init") - 1
	stubs := `proc init {} {}; proc reset {args} {}; proc load_image {file} {echo "SLEPOK_RAM_LOAD=$file"}; proc verify_image {file} {}; proc reg {args} {}; proc mww {args} {}; proc resume {address} {echo "SLEPOK_RAM_RESUME=$address"}; proc program {args} {error "Flash programming forbidden"}`
	args := append([]string(nil), p.Args[:index]...)
	args = append(args, "-c", stubs)
	args = append(args, p.Args[index:]...)
	cmd := process.Command(t.Context(), p.Executable, args...)
	cmd.Dir, cmd.Env = p.Directory, e.buildEnviron()
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("SLEPOK_RAM_LOAD="+filepath.ToSlash(p.Firmware))) || !bytes.Contains(out, []byte("SLEPOK_RAM_RESUME=0x20000000")) {
		t.Fatalf("RAM Tcl parsing failed: %v\n%s", err, out)
	}
}
