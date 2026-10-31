package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/miloVV13/slepok/internal/runner"
)

var runCmd = &command{
	name: "run", args: "--ram [--elf file] [--serial id] [--speed kHz] [--cmsis-dap-quirk] [--no-build] [--dry-run]",
	summary: "load, verify and start RP2040 firmware in SRAM through SWD",
	flags: func(fs *flag.FlagSet) {
		flashCmd.flags(fs)
		fs.Lookup("elf").Usage = "Pico SDK no_flash ELF; default: the single build_dir/ram/*.elf"
		fs.Lookup("no-build").Usage = "load an existing RAM ELF without rebuilding"
		fs.Bool("ram", false, "load and run from RP2040 SRAM without programming Flash")
	},
	run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		if !fs.Lookup("ram").Value.(flag.Getter).Get().(bool) {
			return &usageError{"run currently requires --ram"}
		}
		p, env, err := loadEnv(ctx)
		if err != nil {
			return err
		}
		if p.Config.Profile != "rp2040" {
			return fmt.Errorf("RAM execution currently supports only the rp2040 profile")
		}
		options := runner.FlashOptions{
			Firmware: fs.Lookup("elf").Value.String(), Probe: fs.Lookup("probe").Value.String(),
			Serial: fs.Lookup("serial").Value.String(), Speed: fs.Lookup("speed").Value.(flag.Getter).Get().(int),
			CMSISDAPQuirk: fs.Lookup("cmsis-dap-quirk").Value.(flag.Getter).Get().(bool),
		}
		if options.Probe != "cmsis-dap" || options.Speed <= 0 || options.Speed > 50000 {
			return fmt.Errorf("use --probe cmsis-dap and --speed between 1 and 50000 kHz")
		}
		if env.Packages["openocd"] == "" {
			return fmt.Errorf("OpenOCD is missing from this lock; run slepok init")
		}
		build := filepath.Join(p.BuildDir(), "ram")
		dryRun := fs.Lookup("dry-run").Value.(flag.Getter).Get().(bool)
		if !fs.Lookup("no-build").Value.(flag.Getter).Get().(bool) && !dryRun {
			if _, err := runner.Build(env, p.Dir, build, runner.Options{Context: ctx, RAM: true, Isolate: true, Stdout: os.Stdout, Stderr: os.Stderr, Warn: os.Stderr}); err != nil {
				return err
			}
		}
		plan, err := runner.PlanRP2040RAM(env, p.Dir, build, options)
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Println(plan)
			return nil
		}
		fmt.Printf("Loading %s into RP2040 SRAM through %s (%d kHz); verify and run, no Flash programming.\n", plan.Firmware, options.Probe, options.Speed)
		return runner.Flash(ctx, env, plan, os.Stdout, os.Stderr)
	},
}
