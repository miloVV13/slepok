package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/miloVV13/slepok/internal/runner"
)

var flashCmd = &command{
	name: "flash", args: "[--elf file] [--probe cmsis-dap] [--serial id] [--speed kHz] [--cmsis-dap-quirk] [--no-build] [--dry-run]",
	summary: "build and flash RP2040 through SWD, verify and reset",
	flags: func(fs *flag.FlagSet) {
		fs.String("elf", "", "ARM ELF to flash; default: the single build/*.elf")
		fs.String("probe", "cmsis-dap", "SWD adapter (cmsis-dap)")
		fs.String("serial", "", "select a probe by USB serial number")
		fs.Int("speed", 5000, "SWD clock in kHz")
		fs.Bool("cmsis-dap-quirk", false, "enable CMSIS-DAP compatibility workarounds, including disabling packet pipelining")
		fs.Bool("no-build", false, "flash existing firmware without rebuilding")
		fs.Bool("dry-run", false, "show the invocation without building or accessing USB; requires an existing ELF")
	},
	run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		p, env, err := loadEnv(ctx)
		if err != nil {
			return err
		}
		if p.Config.Profile != "rp2040" {
			return fmt.Errorf("SWD flashing currently supports only the rp2040 profile")
		}
		probe, serial := fs.Lookup("probe").Value.String(), fs.Lookup("serial").Value.String()
		speed := fs.Lookup("speed").Value.(flag.Getter).Get().(int)
		noBuild := fs.Lookup("no-build").Value.(flag.Getter).Get().(bool)
		dryRun := fs.Lookup("dry-run").Value.(flag.Getter).Get().(bool)
		if probe != "cmsis-dap" || speed <= 0 || speed > 50000 {
			return fmt.Errorf("use --probe cmsis-dap and --speed between 1 and 50000 kHz")
		}
		if env.Packages["openocd"] == "" {
			return fmt.Errorf("OpenOCD is missing from this lock; run slepok init and repack to include flashing tools")
		}
		if !noBuild && !dryRun {
			if _, err := runner.Build(env, p.Dir, p.BuildDir(), runner.Options{Context: ctx, Isolate: true, Stdout: os.Stdout, Stderr: os.Stderr, Warn: os.Stderr}); err != nil {
				return err
			}
		}
		plan, err := runner.PlanRP2040Flash(env, p.Dir, p.BuildDir(), runner.FlashOptions{
			Firmware: fs.Lookup("elf").Value.String(), Probe: probe, Serial: serial, Speed: speed,
			CMSISDAPQuirk: fs.Lookup("cmsis-dap-quirk").Value.(flag.Getter).Get().(bool),
		})
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Println(plan)
			return nil
		}
		fmt.Printf("Flashing %s through %s (%d kHz); verify and reset enabled.\n", plan.Firmware, probe, speed)
		return runner.Flash(ctx, env, plan, os.Stdout, os.Stderr)
	},
}
