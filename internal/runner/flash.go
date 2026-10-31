package runner

import (
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/miloVV13/slepok/internal/process"
)

// FlashPlan describes a single OpenOCD invocation. Creating it never opens USB.
type FlashPlan struct {
	Executable string
	Directory  string
	Args       []string
	Firmware   string
}

type FlashOptions struct {
	Firmware      string
	Probe         string
	Serial        string
	Speed         int
	CMSISDAPQuirk bool
}

// PlanRP2040Flash selects an unambiguous ARM ELF and the locked OpenOCD.
// Explicit firmware paths are relative to dir, not the build directory.
func PlanRP2040Flash(e *Env, dir, build string, options FlashOptions) (*FlashPlan, error) {
	firmware, probe, serial, speed := options.Firmware, options.Probe, options.Serial, options.Speed
	if probe != "cmsis-dap" {
		return nil, fmt.Errorf("unsupported RP2040 probe %q; use cmsis-dap", probe)
	}
	if speed <= 0 || speed > 50000 {
		return nil, fmt.Errorf("adapter speed must be between 1 and 50000 kHz")
	}
	if strings.ContainsRune(serial, 0) {
		return nil, fmt.Errorf("probe serial contains a NUL character")
	}
	root := e.Packages["openocd"]
	if root == "" {
		return nil, fmt.Errorf("OpenOCD is missing from this lock; run slepok init and repack to include flashing tools")
	}
	toolEnv := &Env{Path: []string{filepath.Join(root, "bin")}}
	executable, err := toolEnv.LookPath("openocd")
	if err != nil {
		return nil, err
	}
	scripts := filepath.Join(root, "openocd", "scripts")
	for _, rel := range []string{"interface/cmsis-dap.cfg", "target/rp2040.cfg"} {
		info, err := os.Stat(filepath.Join(scripts, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("OpenOCD package is missing %s", rel)
		}
	}
	if firmware == "" {
		entries, err := os.ReadDir(build)
		if err != nil {
			return nil, fmt.Errorf("read build directory: %w; run slepok build first", err)
		}
		var matches []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".elf") {
				matches = append(matches, filepath.Join(build, entry.Name()))
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("found %d ELF files in %s; build first or select one with --elf", len(matches), build)
		}
		firmware = matches[0]
	} else if !filepath.IsAbs(firmware) {
		firmware = filepath.Join(dir, firmware)
	}
	firmware, err = filepath.Abs(firmware)
	if err != nil {
		return nil, err
	}
	image, err := elf.Open(firmware)
	if err != nil {
		return nil, fmt.Errorf("firmware must be an ARM ELF executable: %w", err)
	}
	valid := image.Machine == elf.EM_ARM && image.Type == elf.ET_EXEC
	_ = image.Close()
	if !valid {
		return nil, fmt.Errorf("firmware must be an ARM ELF executable")
	}
	args := []string{"-s", scripts, "-c", "gdb port disabled", "-c", "tcl port disabled", "-c", "telnet port disabled",
		"-f", filepath.Join(scripts, "interface", "cmsis-dap.cfg"), "-c", "transport select swd", "-c", "adapter speed " + strconv.Itoa(speed)}
	if options.CMSISDAPQuirk {
		args = append(args, "-c", "cmsis-dap quirk enable")
	}
	if serial != "" {
		args = append(args, "-c", "adapter serial "+tclWord(serial))
	}
	args = append(args, "-f", filepath.Join(scripts, "target", "rp2040.cfg"), "-c", "program "+tclWord(filepath.ToSlash(firmware))+" verify reset exit")
	return &FlashPlan{Executable: executable, Directory: root, Args: args, Firmware: firmware}, nil
}

// tclWord quotes a literal argument, including paths with Tcl substitutions.
func tclWord(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, `[`, `\[`, `]`, `\]`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func (p *FlashPlan) String() string {
	words := []string{strconv.Quote(p.Executable)}
	for _, arg := range p.Args {
		words = append(words, strconv.Quote(arg))
	}
	return strings.Join(words, " ")
}

// Flash runs without a shell, inheriting only the controlled tool environment.
// It deliberately has USB access; build network isolation is not applied here.
func Flash(ctx context.Context, e *Env, p *FlashPlan, stdout, stderr io.Writer) error {
	cmd := process.Command(ctx, p.Executable, p.Args...)
	// OpenOCD's script search includes cwd. Keep it inside the locked package,
	// so project-local target/interface scripts cannot shadow bundled scripts.
	cmd.Dir, cmd.Stdout, cmd.Stderr = p.Directory, stdout, stderr
	cmd.Env = e.buildEnviron()
	// Keep any bundled Windows runtime DLLs discoverable without host PATH.
	if runtime.GOOS == "windows" {
		cmd.Env = (&Env{Path: append([]string{filepath.Dir(p.Executable)}, e.Path...), Vars: e.Vars}).buildEnviron()
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("OpenOCD: %w; check probe firmware, SWD wiring, target power and USB permissions/drivers (README: Flashing RP2040 and USB access)", err)
	}
	return nil
}
