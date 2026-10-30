package runner

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestShellArgumentsStayLiteral(t *testing.T) {
	args := []string{"-S", "a space & 'quote' %value%!", "-B", "build"}
	flags := []string{"-DNAME=a space & 'quote' %value%!"}
	want := append(append([]string{}, args...), flags...)
	got := shellCMakeArgs(args, flags)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v", got)
	}
	got[0] = "changed"
	if args[0] != "-S" {
		t.Fatal("mutated input")
	}
	for _, mode := range []string{"--build", "--install", "-E", "-Pscript.cmake", "--help-command", "--list-presets", "--version"} {
		in := []string{mode, "a space"}
		if got := shellCMakeArgs(in, flags); !reflect.DeepEqual(got, in) {
			t.Fatalf("%s: %#v", mode, got)
		}
	}
}

func TestShellConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	file, cmake := filepath.Join(dir, "config.json"), filepath.Join(dir, "a & b", "cmake.exe")
	args := []string{"-DVALUE=a space & 'quote'"}
	if err := writeShellConfig(file, cmake, args); err != nil {
		t.Fatal(err)
	}
	cfg, err := readShellConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CMake != cmake || !reflect.DeepEqual(cfg.Configure, args) {
		t.Fatalf("%+v", cfg)
	}
	if err := writeShellConfig(file, "relative.exe", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := readShellConfig(file); err == nil {
		t.Fatal("accepted relative executable")
	}
}
