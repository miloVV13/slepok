package runner

import (
	"context"
	"debug/elf"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/miloVV13/slepok/internal/process"
	"github.com/miloVV13/slepok/internal/safepath"
	"github.com/miloVV13/slepok/internal/store"
)

//go:embed esp32_monitor.py
var esp32MonitorBridge []byte

// SerialPlan is an argv invocation of the locked Python and libraries.
// Planning never enumerates or opens ports, and never executes Python.
type SerialPlan struct {
	Executable, Directory string
	Args                  []string
	Files                 []FlashFile
}

type FlashFile struct {
	Offset uint64
	Path   string
	Size   uint64
}

func (p *SerialPlan) String() string {
	words := []string{strconv.Quote(p.Executable)}
	for _, a := range p.Args {
		words = append(words, strconv.Quote(a))
	}
	return strings.Join(words, " ")
}

func ValidateSerialOptions(port string, baud int) error {
	if strings.TrimSpace(port) == "" || strings.HasPrefix(port, "-") || strings.Contains(port, "://") || strings.ContainsAny(port, "\x00\r\n") {
		return fmt.Errorf("--port must name a local serial port, e.g. /dev/ttyUSB0 or COM3; use slepok ports to list ports")
	}
	if baud < 300 || baud > 4000000 {
		return fmt.Errorf("--baud must be between 300 and 4000000")
	}
	return nil
}

func serialPython(e *Env) (*SerialPlan, error) {
	root := e.Packages["python"]
	if root == "" || e.Packages["esp-idf-python"] == "" {
		return nil, fmt.Errorf("locked ESP-IDF Python environment is missing; run slepok init or unpack an ESP32 snapshot")
	}
	name := filepath.Join(root, "bin", "python3")
	if runtime.GOOS == "windows" {
		name = filepath.Join(root, "python.exe")
	}
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("locked Python executable is missing: %s", name)
	}
	return &SerialPlan{Executable: name, Directory: root}, nil
}

// PlanESP32Flash consumes IDF's generated offsets rather than assuming a
// partition layout. Only ordinary, unencrypted classic ESP32 images are supported.
func PlanESP32Flash(e *Env, build, port string, baud int) (*SerialPlan, error) {
	if err := ValidateSerialOptions(port, baud); err != nil {
		return nil, err
	}
	p, err := serialPython(e)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(build, "flasher_args.json"))
	if err != nil {
		return nil, fmt.Errorf("read flasher_args.json: %w; run slepok build first", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 1<<20 {
		return nil, fmt.Errorf("flasher_args.json exceeds 1 MiB")
	}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("flasher_args.json: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("flasher_args.json: trailing data or oversized metadata")
	}
	var meta struct {
		WriteArgs []string          `json:"write_flash_args"`
		Files     map[string]string `json:"flash_files"`
		Extra     struct {
			Chip, Before, After string
			Stub                *bool
		} `json:"extra_esptool_args"`
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.Extra.Chip != "esp32" || meta.Extra.Stub == nil || !*meta.Extra.Stub || meta.Extra.Before != "default_reset" || meta.Extra.After != "hard_reset" {
		return nil, fmt.Errorf("unsupported ESP32 flashing configuration: requires esp32, stub, default_reset and hard_reset")
	}
	allowed := map[string][]string{
		"--flash_mode": {"dio", "dout", "qio", "qout", "keep"},
		"--flash_freq": {"20m", "26m", "40m", "80m", "keep"},
		"--flash_size": {"1MB", "2MB", "4MB", "8MB", "16MB", "keep", "detect"},
	}
	seen := map[string]bool{}
	limit := uint64(16 << 20)
	if len(meta.WriteArgs) != 6 {
		return nil, fmt.Errorf("unsupported write_flash_args; expected flash mode, frequency and size")
	}
	for i := 0; i < len(meta.WriteArgs); i += 2 {
		key, value := meta.WriteArgs[i], meta.WriteArgs[i+1]
		if seen[key] || !slices.Contains(allowed[key], value) {
			return nil, fmt.Errorf("unsupported flash setting %s %s", key, value)
		}
		seen[key] = true
		if key == "--flash_size" && strings.HasSuffix(value, "MB") {
			n, _ := strconv.ParseUint(strings.TrimSuffix(value, "MB"), 10, 64)
			limit = n << 20
		}
	}
	// IDF can emit additional entries (for example encrypted NVS).
	// Reject encryption on every entry, not just the three mandatory images.
	for key, value := range raw {
		var entry struct{ Encrypted json.RawMessage }
		if json.Unmarshal(value, &entry) == nil && len(entry.Encrypted) != 0 && string(entry.Encrypted) != `"false"` && string(entry.Encrypted) != "false" {
			return nil, fmt.Errorf("encrypted %s flashing is unsupported", key)
		}
	}
	if len(meta.Files) < 3 {
		return nil, fmt.Errorf("flash metadata must include bootloader, partition table and application")
	}
	for offset, name := range meta.Files {
		address, err := strconv.ParseUint(offset, 0, 32)
		if err != nil || address%4096 != 0 {
			return nil, fmt.Errorf("invalid or unaligned flash offset %q", offset)
		}
		file, err := buildFile(build, name)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		size := uint64(info.Size())
		if size == 0 || address >= limit || size > limit-address {
			return nil, fmt.Errorf("flash file %s is empty or exceeds configured flash size", name)
		}
		p.Files = append(p.Files, FlashFile{address, file, size})
	}
	slices.SortFunc(p.Files, func(a, b FlashFile) int {
		if a.Offset < b.Offset {
			return -1
		}
		if a.Offset > b.Offset {
			return 1
		}
		return 0
	})
	for i := 1; i < len(p.Files); i++ {
		previous := p.Files[i-1]
		if p.Files[i].Offset < (previous.Offset+previous.Size+4095)&^uint64(4095) {
			return nil, fmt.Errorf("flash files have overlapping erase sectors")
		}
	}
	mandatoryOffsets := map[string]bool{}
	for _, key := range []string{"bootloader", "app", "partition-table"} {
		var entry struct {
			Offset, File string
			Encrypted    json.RawMessage
		}
		if err := json.Unmarshal(raw[key], &entry); err != nil {
			return nil, fmt.Errorf("flash metadata missing %s entry", key)
		}
		if string(entry.Encrypted) != `"false"` && string(entry.Encrypted) != "false" {
			return nil, fmt.Errorf("encrypted or unspecified %s flashing is unsupported", key)
		}
		if mandatoryOffsets[entry.Offset] || meta.Files[entry.Offset] != entry.File || entry.File == "" {
			return nil, fmt.Errorf("inconsistent %s flash entry", key)
		}
		mandatoryOffsets[entry.Offset] = true
		if key != "partition-table" {
			file, err := buildFile(build, entry.File)
			if err != nil {
				return nil, err
			}
			image, err := os.Open(file)
			if err != nil {
				return nil, err
			}
			var header [24]byte
			_, err = io.ReadFull(image, header[:])
			_ = image.Close()
			if err != nil || header[0] != 0xe9 || binary.LittleEndian.Uint16(header[12:14]) != 0 {
				return nil, fmt.Errorf("%s must be a classic ESP32 binary image", key)
			}
		}
	}
	// No --force, erase_flash, encryption or arbitrary user-controlled tool flags.
	p.Args = []string{"-m", "esptool", "--chip", "esp32", "--port", port, "--baud", strconv.Itoa(baud), "--before", "default_reset", "--after", "hard_reset", "write_flash"}
	p.Args = append(p.Args, meta.WriteArgs...)
	for _, file := range p.Files {
		p.Args = append(p.Args, fmt.Sprintf("0x%x", file.Offset), file.Path)
	}
	return p, nil
}

func buildFile(build, name string) (string, error) {
	if err := safepath.CheckForHost(name); err != nil {
		return "", fmt.Errorf("invalid build artifact %q: %w", name, err)
	}
	root, err := filepath.EvalSymlinks(build)
	if err != nil {
		return "", err
	}
	file, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("build artifact %q escapes build directory", name)
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("build artifact %q must be a regular file", name)
	}
	return file, nil
}

func PlanESP32Ports(e *Env) (*SerialPlan, error) {
	p, err := serialPython(e)
	if err != nil {
		return nil, err
	}
	p.Args = []string{"-m", "serial.tools.list_ports", "-v"}
	return p, nil
}

func PlanESP32Monitor(ctx context.Context, e *Env, st *store.Store, dir, build, firmware, port string, baud int, reset, timestamps bool) (*SerialPlan, error) {
	if err := ValidateSerialOptions(port, baud); err != nil {
		return nil, err
	}
	p, err := serialPython(e)
	if err != nil {
		return nil, err
	}
	if firmware == "" {
		files, err := filepath.Glob(filepath.Join(build, "*.elf"))
		if err != nil {
			return nil, err
		}
		if len(files) != 1 {
			return nil, fmt.Errorf("monitor requires one build/*.elf; run slepok build or use --elf")
		}
		firmware = files[0]
	} else if !filepath.IsAbs(firmware) {
		firmware = filepath.Join(dir, firmware)
	}
	firmware, err = filepath.Abs(firmware)
	if err != nil {
		return nil, err
	}
	image, err := elf.Open(firmware)
	if err != nil {
		return nil, fmt.Errorf("monitor ELF: %w", err)
	}
	valid := image.Class == elf.ELFCLASS32 && image.Data == elf.ELFDATA2LSB && image.Type == elf.ET_EXEC && image.Machine == elf.EM_XTENSA
	_ = image.Close()
	if !valid {
		return nil, fmt.Errorf("monitor needs an ELF32 classic Xtensa executable")
	}
	work, err := st.TempDir("monitor-bridge-")
	if err != nil {
		return nil, err
	}
	defer store.RemoveTree(work)
	if err := os.WriteFile(filepath.Join(work, "monitor.py"), esp32MonitorBridge, 0644); err != nil {
		return nil, err
	}
	digest, err := st.InstallDirContext(ctx, work, "")
	if err != nil {
		return nil, err
	}
	bridge, err := st.Path(digest)
	if err != nil {
		return nil, err
	}
	p.Directory = dir
	p.Args = []string{filepath.Join(bridge, "monitor.py"), "--port", port, "--baud", strconv.Itoa(baud), "--target", "esp32", "--toolchain-prefix", "xtensa-esp32-elf-"}
	if !reset {
		p.Args = append(p.Args, "--no-reset")
	}
	if timestamps {
		p.Args = append(p.Args, "--timestamps")
	}
	p.Args = append(p.Args, firmware)
	return p, nil
}

func RunSerial(ctx context.Context, e *Env, p *SerialPlan, stdin io.Reader, stdout, stderr io.Writer, interactive bool) error {
	cmd := process.Command(ctx, p.Executable, p.Args...)
	if interactive {
		cmd = process.InteractiveCommand(ctx, p.Executable, p.Args...)
	}
	cmd.Dir, cmd.Env = p.Directory, e.buildEnviron()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ESP32 serial tool: %w; check --port, USB/UART drivers and port access; close other serial programs (docs/ESP32.md)", err)
	}
	return nil
}
