package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/runner"
)

func boolFlag(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}
func intFlag(fs *flag.FlagSet, name string) int {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int)
}

func rejectExplicitFlags(fs *flag.FlagSet, names ...string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		for _, name := range names {
			if f.Name == name {
				err = fmt.Errorf("--%s is not supported for this MCU/transport", name)
			}
		}
	})
	return err
}

func flashESP32(ctx context.Context, fs *flag.FlagSet, p *project.Project, e *runner.Env) error {
	if err := rejectExplicitFlags(fs, "elf", "probe", "serial", "speed", "cmsis-dap-quirk"); err != nil {
		return err
	}
	port, baud := fs.Lookup("port").Value.String(), intFlag(fs, "baud")
	if err := runner.ValidateSerialOptions(port, baud); err != nil {
		return err
	}
	if !boolFlag(fs, "no-build") && !boolFlag(fs, "dry-run") {
		if _, err := runner.Build(e, p.Dir, p.BuildDir(), runner.Options{Context: ctx, Isolate: true, Stdout: os.Stdout, Stderr: os.Stderr, Warn: os.Stderr}); err != nil {
			return err
		}
	}
	plan, err := runner.PlanESP32Flash(e, p.BuildDir(), port, baud)
	if err != nil {
		return err
	}
	if boolFlag(fs, "dry-run") {
		fmt.Println(plan)
		return nil
	}
	fmt.Printf("Flashing %d ESP32 images on %s (%d baud); esptool verifies writes and resets the target.\n", len(plan.Files), port, baud)
	return runner.RunSerial(ctx, e, plan, nil, os.Stdout, os.Stderr, false)
}

var portsCmd = &command{
	name: "ports", args: "[--dry-run]", summary: "list local serial ports using the locked ESP32 Python tools",
	flags: func(fs *flag.FlagSet) { fs.Bool("dry-run", false, "show the invocation without enumerating devices") },
	run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		p, e, err := loadEnv(ctx)
		if err != nil {
			return err
		}
		if p.Config.Profile != "esp32" {
			return fmt.Errorf("serial port listing currently requires the esp32 profile")
		}
		plan, err := runner.PlanESP32Ports(e)
		if err != nil {
			return err
		}
		if boolFlag(fs, "dry-run") {
			fmt.Println(plan)
			return nil
		}
		return runner.RunSerial(ctx, e, plan, nil, os.Stdout, os.Stderr, false)
	},
}

var monitorCmd = &command{
	name: "monitor", args: "--port PORT [--baud rate] [--elf file] [--reset] [--timestamps] [--dry-run]",
	summary: "open the ESP32 serial monitor; Ctrl+] exits (does not build or flash)",
	flags: func(fs *flag.FlagSet) {
		fs.String("port", "", "local serial port, e.g. /dev/ttyUSB0 or COM3")
		fs.Int("baud", 115200, "monitor baud rate")
		fs.String("elf", "", "ELF for address decoding; default: the single build/*.elf")
		fs.Bool("reset", false, "reset the chip when starting the monitor")
		fs.Bool("timestamps", false, "prefix lines with timestamps")
		fs.Bool("dry-run", false, "show the invocation without opening the serial port")
	},
	run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
		if err := maxArgs(args, 0); err != nil {
			return err
		}
		p, e, err := loadEnv(ctx)
		if err != nil {
			return err
		}
		if p.Config.Profile != "esp32" {
			return fmt.Errorf("serial monitoring currently requires the esp32 profile")
		}
		st, err := openStore()
		if err != nil {
			return err
		}
		plan, err := runner.PlanESP32Monitor(ctx, e, st, p.Dir, p.BuildDir(), fs.Lookup("elf").Value.String(), fs.Lookup("port").Value.String(), intFlag(fs, "baud"), boolFlag(fs, "reset"), boolFlag(fs, "timestamps"))
		if err != nil {
			return err
		}
		if boolFlag(fs, "dry-run") {
			fmt.Println(plan)
			return nil
		}
		fmt.Fprintln(os.Stderr, "ESP32 monitor: Ctrl+] exits; Ctrl+T opens the menu. Build/flash shortcuts are disabled.")
		return runner.RunSerial(ctx, e, plan, os.Stdin, os.Stdout, os.Stderr, true)
	},
}
