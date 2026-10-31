package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

func TestInitMCUDownloadsOnlySelectedTarget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SLEPOK_HOME", filepath.Join(root, "store"))
	t.Cleanup(func() { _ = store.RemoveTree(filepath.Join(root, "store")) })
	file := filepath.Join(root, "tool")
	must(t, os.WriteFile(file, []byte("SDK fixture"), 0644))
	archive := toolchainArchive(t, file)
	sum := sha256.Sum256(archive)
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	fs := fstest.MapFS{}
	for _, id := range []string{"rp2040", "stm32f103c8"} {
		text := fmt.Sprintf("id=%q\nversion=\"1\"\nboards=[\"board\"]\n[[components]]\nname=%q\nversion=\"1\"\n[sources]\n", id, id+"-sdk")
		text = strings.TrimSuffix(text, "[sources]\n") + fmt.Sprintf("[components.sources.any]\nurl=%q\nsha256=\"%x\"\nformat=\"tar.gz\"\nstrip=1\n", srv.URL+"/"+id, sum)
		fs["profiles/"+id+".toml"] = &fstest.MapFile{Data: []byte(text)}
	}
	saved := profile.FS
	profile.FS = fs
	t.Cleanup(func() { profile.FS = saved })
	dir := filepath.Join(root, "project")
	must(t, os.MkdirAll(dir, 0755))
	must(t, os.WriteFile(filepath.Join(dir, "main.c"), []byte("int main(void){}"), 0644))
	must(t, execute([]string{"init", "--mcu", "STM32F103C8T6", dir}))
	p, err := project.Load(dir)
	must(t, err)
	if p.Config.Profile != "stm32f103c8" || len(p.Lock.Packages) != 1 || p.Lock.Packages[0].Name != "stm32f103c8-sdk" {
		t.Fatalf("wrong target packages: %+v", p)
	}
	mu.Lock()
	got := append([]string(nil), requests...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "/stm32f103c8" {
		t.Fatalf("downloaded packages of another MCU: %v", got)
	}
	// Re-init without a flag keeps the existing target rather than the default.
	must(t, execute([]string{"init", dir}))
	mustFail(t, execute([]string{"init", "--mcu", "rp2040", dir}), "already sets profile")
	conflict := filepath.Join(root, "conflict")
	mustFail(t, execute([]string{"init", "--mcu", "rp2040", "--profile", "stm32f103c8", conflict}), "different targets")
	if _, err := os.Stat(conflict); !os.IsNotExist(err) {
		t.Fatal("conflicting target flags changed the filesystem")
	}
}
