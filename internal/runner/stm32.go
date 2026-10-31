package runner

import (
	"debug/elf"
	"fmt"
	"path/filepath"
)

// PlanSTM32F103Flash accepts only loadable data for the C8's 64 KiB Flash.
func PlanSTM32F103Flash(e *Env, dir, build string, options FlashOptions) (*FlashPlan, error) {
	p, err := prepareOpenOCD(e, dir, build, options, "stm32f1x")
	if err != nil {
		return nil, err
	}
	f, err := elf.Open(p.Firmware)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const flashStart, flashEnd = uint64(0x08000000), uint64(0x08010000)
	loaded, entry := false, false
	for _, segment := range f.Progs {
		if segment.Type != elf.PT_LOAD {
			continue
		}
		inFlash := segment.Vaddr >= flashStart && segment.Vaddr < flashEnd && segment.Memsz <= flashEnd-segment.Vaddr
		inRAM := segment.Vaddr >= 0x20000000 && segment.Vaddr < 0x20005000 && segment.Memsz <= 0x20005000-segment.Vaddr
		if segment.Filesz > segment.Memsz || (!inFlash && !inRAM) {
			return nil, fmt.Errorf("ELF runtime segment exceeds STM32F103C8 memory (64 KiB Flash, 20 KiB SRAM)")
		}
		if segment.Filesz == 0 {
			continue
		}
		if segment.Paddr < flashStart || segment.Paddr >= flashEnd || segment.Filesz > flashEnd-segment.Paddr {
			return nil, fmt.Errorf("ELF load addresses exceed STM32F103C8 Flash (64 KiB at 0x08000000)")
		}
		loaded = true
		pc := f.Entry &^ 1
		if segment.Flags&elf.PF_X != 0 && pc >= segment.Vaddr && pc-segment.Vaddr < segment.Filesz {
			entry = true
		}
	}
	if f.Class != elf.ELFCLASS32 || f.Data != elf.ELFDATA2LSB || !loaded || !entry || f.Entry&1 == 0 || f.Entry < flashStart || f.Entry >= flashEnd {
		return nil, fmt.Errorf("ELF must contain an STM32F103C8 Flash image and a Thumb entry point in Flash")
	}
	p.Args = append(p.Args, "-c", "program "+tclWord(filepath.ToSlash(p.Firmware))+" verify reset exit")
	return p, nil
}
