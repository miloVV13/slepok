package runner

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"path/filepath"
)

const rp2040RAMStart, rp2040RAMEnd = uint64(0x20000000), uint64(0x20042000)

// RAMImage contains startup metadata for a standard Pico SDK no_flash ELF.
type RAMImage struct {
	Entry, Vectors, Stack uint64
}

func inRAM(address, size uint64) bool {
	return address >= rp2040RAMStart && address < rp2040RAMEnd && size <= rp2040RAMEnd-address
}

// InspectRP2040RAM rejects flash/copy_to_ram/mixed images before USB is opened.
// ELF physical addresses matter: OpenOCD load_image uses the load addresses.
func InspectRP2040RAM(file string) (*RAMImage, error) {
	f, err := elf.Open(file)
	if err != nil {
		return nil, fmt.Errorf("RAM image: %w", err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS32 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_EXEC || f.Machine != elf.EM_ARM {
		return nil, fmt.Errorf("RAM image must be a little-endian ELF32 ARM executable")
	}
	loaded := 0
	var ranges [][2]uint64
	executable := func(address uint64) bool {
		for _, p := range f.Progs {
			if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 && address >= p.Vaddr && address-p.Vaddr < p.Filesz {
				return true
			}
		}
		return false
	}
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD {
			continue
		}
		if p.Filesz > p.Memsz || p.Vaddr != p.Paddr || !inRAM(p.Paddr, p.Memsz) {
			return nil, fmt.Errorf("RAM image has a load segment outside RP2040 SRAM or with different load/run addresses (0x%x → 0x%x, %d bytes); use a Pico SDK no_flash build", p.Paddr, p.Vaddr, p.Memsz)
		}
		if p.Memsz > 0 {
			for _, previous := range ranges {
				if p.Paddr < previous[1] && previous[0] < p.Paddr+p.Memsz {
					return nil, fmt.Errorf("RAM image has overlapping load segments")
				}
			}
			ranges = append(ranges, [2]uint64{p.Paddr, p.Paddr + p.Memsz})
		}
		if p.Filesz > 0 {
			loaded++
		}
	}
	if loaded == 0 || f.Entry&1 == 0 || !executable(f.Entry&^1) {
		return nil, fmt.Errorf("RAM image needs a Thumb entry point inside a loaded executable SRAM segment")
	}
	symbols, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf("RAM image needs Pico SDK startup symbols; use an unstripped ELF: %w", err)
	}
	var vectors, entry uint64
	for _, symbol := range symbols {
		switch symbol.Name {
		case "__vectors":
			vectors = symbol.Value
		case "_entry_point":
			entry = symbol.Value
		}
	}
	if entry != f.Entry || !inRAM(vectors, 8) || vectors&255 != 0 {
		return nil, fmt.Errorf("RAM image needs the Pico SDK _entry_point and aligned __vectors symbols")
	}
	var vector [8]byte
	found := false
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && vectors >= p.Vaddr && vectors-p.Vaddr <= p.Filesz && p.Filesz-(vectors-p.Vaddr) >= 8 {
			if _, err := p.ReadAt(vector[:], int64(vectors-p.Vaddr)); err != nil {
				return nil, err
			}
			found = true
			break
		}
	}
	stack, reset := uint64(binary.LittleEndian.Uint32(vector[:4])), uint64(binary.LittleEndian.Uint32(vector[4:]))
	if !found || stack <= rp2040RAMStart || stack > rp2040RAMEnd || stack&7 != 0 || reset&1 == 0 || !executable(reset&^1) {
		return nil, fmt.Errorf("RAM image has invalid SRAM stack or reset vector")
	}
	return &RAMImage{Entry: f.Entry, Vectors: vectors, Stack: stack}, nil
}

// PlanRP2040RAM uses only memory loading/verification and CPU startup commands.
// The secondary core waits in boot ROM after reset; no SMP resume is used.
func PlanRP2040RAM(e *Env, dir, build string, options FlashOptions) (*FlashPlan, error) {
	p, err := prepareRP2040OpenOCD(e, dir, build, options)
	if err != nil {
		return nil, err
	}
	image, err := InspectRP2040RAM(p.Firmware)
	if err != nil {
		return nil, err
	}
	// The common configuration ends with -f target/rp2040.cfg.
	args := append([]string(nil), p.Args[:len(p.Args)-2]...)
	args = append(args, "-c", "set USE_CORE 0", "-f", p.Args[len(p.Args)-1])
	commands := []string{
		"init", "reset halt",
		"load_image " + tclWord(filepath.ToSlash(p.Firmware)),
		"verify_image " + tclWord(filepath.ToSlash(p.Firmware)),
		"reg msp " + fmt.Sprintf("0x%x", image.Stack), "reg psp 0", "reg control 0", "reg primask 0", "reg xpsr 0x01000000",
		fmt.Sprintf("mww 0xe000ed08 0x%x", image.Vectors),
		fmt.Sprintf("resume 0x%x", image.Entry&^1), "shutdown",
	}
	for _, command := range commands {
		args = append(args, "-c", command)
	}
	p.Args = args
	return p, nil
}
