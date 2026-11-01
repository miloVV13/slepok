package runner

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func shortPath(t *testing.T, p string) string {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(p)
	must(t, err)
	buffer := make([]uint16, 32769)
	n, err := syscall.GetShortPathName(name, &buffer[0], uint32(len(buffer)))
	must(t, err)
	if n == 0 || n >= uint32(len(buffer)) {
		t.Fatal("invalid short path length")
	}
	return syscall.UTF16ToString(buffer[:n])
}

func TestWindowsShortToolPathsRespectStoreBoundaries(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store with spaces")
	other := filepath.Join(root, "store with spaces-other")
	for _, dir := range []string{store, other} {
		must(t, os.MkdirAll(dir, 0755))
		must(t, os.WriteFile(filepath.Join(dir, "arm-none-eabi-gcc.exe"), []byte("fixture"), 0644))
	}
	tool := filepath.Join(store, "arm-none-eabi-gcc.exe")
	shortTool := shortPath(t, tool)
	shortOther := shortPath(t, filepath.Join(other, "arm-none-eabi-gcc.exe"))
	if !strings.Contains(shortTool, "~") {
		t.Skip("8.3 aliases are disabled on this volume")
	}
	allowed := []string{store}
	if hostPath(shortTool, allowed) || hostPath(strings.ReplaceAll(shortTool, `\`, "/"), allowed) {
		t.Fatal("short name for pinned tool was treated as a host dependency")
	}
	if !hostPath(shortOther, allowed) {
		t.Fatal("short name for an external tool was incorrectly allowed")
	}
	if hostPath(shortTool, nil, tool) || !hostPath(shortOther, nil, tool) {
		t.Fatal("exact-file exception did not preserve its boundary through aliases")
	}
	missing := filepath.Join(root, "MISSING~1", "tool.exe")
	if !hostPath(missing, allowed) {
		t.Fatal("an unresolved short path was exempted")
	}
	cache := filepath.Join(root, "CMakeCache.txt")
	must(t, os.WriteFile(cache, []byte("PINNED:FILEPATH="+shortTool+"\nEXTERNAL:FILEPATH="+shortOther+"\n"), 0644))
	leaks, err := Leaks(cache, allowed)
	must(t, err)
	if len(leaks) != 1 || !strings.Contains(leaks[0], "EXTERNAL") {
		t.Fatalf("wrong cache dependencies: %v", leaks)
	}
	graph := "command = \"" + shortTool + "\" -c main.c\ncommand = \"" + shortOther + "\" -c main.c\n"
	must(t, os.WriteFile(filepath.Join(root, "build.ninja"), []byte(graph), 0644))
	leaks, err = NinjaLeaks(root, allowed)
	must(t, err)
	if len(leaks) != 1 || !strings.Contains(leaks[0], shortOther) {
		t.Fatalf("wrong Ninja dependencies: %v", leaks)
	}
}
