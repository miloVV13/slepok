package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

func TestLockAddsHostWithoutChangingPinnedEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfg := project.Config{Profile: "rp2040", Board: "pico", Platforms: []string{"linux-x64"}, BuildDir: "build"}
	must(t, project.WriteConfig(dir, cfg))
	prof, err := profile.Load("rp2040")
	must(t, err)
	lock, err := lockFor(dir, cfg, prof)
	must(t, err)
	lock.SourceDateEpoch = 123456
	// A pinned URL must win over a later profile's source for the same host.
	lock.Packages[0].Source.URL = "https://pinned.invalid/compiler.zip"
	old := append([]project.LockedPackage(nil), lock.Packages...)
	must(t, project.WriteLock(dir, *lock))
	cfg.Platforms = append(cfg.Platforms, "windows-x64")
	must(t, project.WriteConfig(dir, cfg))
	updated, err := lockFor(dir, cfg, prof)
	must(t, err)
	if updated.SourceDateEpoch != 123456 || !reflect.DeepEqual(updated.Packages[:len(old)], old) {
		t.Fatal("adding a host changed pinned Linux sources or build date")
	}
	if len(updated.PackagesFor("windows-x64")) != len(prof.Components) || len(updated.PackagesFor("linux-x64")) != len(old) {
		t.Fatal("added host is incomplete")
	}
	must(t, project.WriteLock(dir, *updated))
	again, err := lockFor(dir, cfg, prof)
	must(t, err)
	if !reflect.DeepEqual(again, updated) {
		t.Fatal("repeated init duplicated or changed host packages")
	}
}

func TestLockRefusesMixedVersionsWhenAddingHost(t *testing.T) {
	dir := t.TempDir()
	cfg := project.Config{Profile: "rp2040", Board: "pico", Platforms: []string{"linux-x64"}, BuildDir: "build"}
	must(t, project.WriteConfig(dir, cfg))
	prof, err := profile.Load("rp2040")
	must(t, err)
	lock, err := lockFor(dir, cfg, prof)
	must(t, err)
	lock.Packages[0].Version = "old-compiler"
	must(t, project.WriteLock(dir, *lock))
	// Keeping the existing platform does not upgrade its compiler.
	_, err = lockFor(dir, cfg, prof)
	must(t, err)
	cfg.Platforms = append(cfg.Platforms, "windows-x64")
	if _, err := lockFor(dir, cfg, prof); err == nil || !strings.Contains(err.Error(), "rather than mixing versions") {
		t.Fatalf("mixed versions accepted: %v", err)
	}
}

func TestPackRejectsIncompleteAddedHost(t *testing.T) {
	prof, err := profile.Load("rp2040")
	must(t, err)
	p := &project.Project{Dir: t.TempDir(), Config: project.Config{Platforms: []string{"windows-x64"}}, Lock: &project.Lock{Packages: []project.LockedPackage{{Name: "pico-sdk", Platform: "any"}}}}
	st, err := store.Open(filepath.Join(t.TempDir(), "store"))
	must(t, err)
	if _, err := snapshotPackages(p, st, prof); err == nil || !strings.Contains(err.Error(), "no locked arm-gcc") {
		t.Fatalf("incomplete Windows snapshot accepted: %v", err)
	}
}
