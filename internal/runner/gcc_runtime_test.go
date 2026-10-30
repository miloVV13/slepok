package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/store"
)

func TestGCCRuntimeHelper(t *testing.T) {
	if os.Getenv("SLEPOK_GCC_RUNTIME_HELPER") != "1" {
		return
	}
	data, _ := json.Marshal(struct {
		Libraries string
		Args      []string
	}{os.Getenv("DYLD_LIBRARY_PATH"), os.Args[1:]})
	fmt.Println(string(data))
	os.Exit(0)
}

func TestGCCRuntimeLaunchersAreLiteralAndRecreated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher")
	}
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { _ = store.RemoveTree(st.Root()) })
	gcc := filepath.Join(t.TempDir(), "gcc & ' quoted")
	must(t, os.MkdirAll(filepath.Join(gcc, "bin"), 0755))
	exe, err := os.Executable()
	must(t, err)
	in, err := os.Open(exe)
	must(t, err)
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(gcc, "bin", "arm-none-eabi-gcc"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	must(t, err)
	_, err = io.Copy(out, in)
	must(t, err)
	must(t, out.Close())
	libs := filepath.Join(t.TempDir(), "runtime & ' libs")
	dir, err := gccRuntimeLaunchers(t.Context(), st, gcc, libs)
	must(t, err)
	args := []string{"-test.run=^TestGCCRuntimeHelper$", "--", "a space & 'quote'"}
	cmd := exec.Command(filepath.Join(dir, "bin", "arm-none-eabi-gcc"), args...)
	cmd.Env = append(os.Environ(), "SLEPOK_GCC_RUNTIME_HELPER=1", "DYLD_LIBRARY_PATH=must-be-replaced")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, data)
	}
	var got struct {
		Libraries string
		Args      []string
	}
	must(t, json.Unmarshal(data, &got))
	if got.Libraries != libs || !reflect.DeepEqual(got.Args, args) {
		t.Fatalf("forwarded: %+v", got)
	}
	must(t, st.Remove("sha256:"+filepath.Base(dir)))
	again, err := gccRuntimeLaunchers(t.Context(), st, gcc, libs)
	must(t, err)
	if again != dir {
		t.Fatal("launcher path changed after GC")
	}
	if _, err := os.Stat(filepath.Join(again, "bin", "arm-none-eabi-gcc")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-be-replaced") {
		t.Fatal("inherited runtime library path")
	}
}
