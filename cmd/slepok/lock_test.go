package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/pkgtar"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/snapshot"
	"github.com/miloVV13/slepok/internal/store"
)

func TestUniversalPackageRestoresOnBothMacArchitectures(t *testing.T) {
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { _ = store.RemoveTree(st.Root()) })
	archive := func(content string) string {
		tree := pkgtar.NewTree(t.TempDir())
		must(t, tree.File(t.Context(), "bin/tool", 0755, strings.NewReader(content)))
		var buf bytes.Buffer
		digest, _, err := tree.Write(t.Context(), &buf)
		must(t, err)
		must(t, st.InstallArchiveContext(t.Context(), digest, bytes.NewReader(buf.Bytes())))
		return digest
	}
	linux, universal := archive("ELF fixture"), archive("universal Mach-O fixture")
	platforms := []string{"linux-x64", "macos-arm64", "macos-x64"}
	p := &project.Project{Config: project.Config{Platforms: platforms}, Lock: &project.Lock{}}
	for _, platform := range platforms {
		digest := universal
		if platform == "linux-x64" {
			digest = linux
		}
		p.Lock.Packages = append(p.Lock.Packages, project.LockedPackage{Name: "tools", Version: "1", Platform: platform, Source: profile.Source{Digest: digest}})
	}
	prof := &profile.Profile{Components: []profile.Component{{Name: "tools", Version: "1"}}}
	packages, err := snapshotPackages(p, st, prof)
	must(t, err)
	if len(packages) != 2 || packages[0].Platform != "linux-x64" || packages[1].Platform != manifest.AnyPlatform {
		t.Fatalf("wrong shared payload metadata: %+v", packages)
	}
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "main.c"), []byte("int main(void){}"), 0644))
	file := filepath.Join(t.TempDir(), "all.slepok")
	_, err = snapshot.Pack(file, snapshot.Options{Context: t.Context(), Profile: manifest.Profile{ID: "rp2040", Version: "1"}, Platforms: platforms, ProjectDir: dir, ProjectFiles: []string{"main.c"}, Packages: packages})
	must(t, err)
	for _, platform := range platforms {
		receiver, err := store.Open(t.TempDir())
		must(t, err)
		t.Cleanup(func() { _ = store.RemoveTree(receiver.Root()) })
		_, err = snapshot.Unpack(file, filepath.Join(t.TempDir(), "project"), receiver, platform)
		must(t, err)
		want, other := universal, linux
		if platform == "linux-x64" {
			want, other = linux, universal
		}
		if !receiver.HasTree(want) || receiver.HasTree(other) {
			t.Fatalf("%s selected wrong package", platform)
		}
	}
}

func TestSnapshotPackagesUsesForeignArchive(t *testing.T) {
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { _ = store.RemoveTree(st.Root()) })
	tree := pkgtar.NewTree(t.TempDir())
	must(t, tree.File(t.Context(), "bin/tool", 0755, strings.NewReader("tool")))
	var buf bytes.Buffer
	digest, _, err := tree.Write(t.Context(), &buf)
	must(t, err)
	must(t, st.InstallArchiveContext(t.Context(), digest, bytes.NewReader(buf.Bytes())))
	prof := &profile.Profile{Components: []profile.Component{{Name: "tools", Version: "1"}}}
	p := &project.Project{Config: project.Config{Platforms: []string{"linux-x64"}}, Lock: &project.Lock{Packages: []project.LockedPackage{{Name: "tools", Version: "1", Platform: "linux-x64", Source: profile.Source{Digest: digest}}}}}
	pkgs, err := snapshotPackages(p, st, prof)
	must(t, err)
	if len(pkgs) != 1 || pkgs[0].Archive == "" || pkgs[0].Dir != "" || pkgs[0].ExpectedDigest != digest {
		t.Fatalf("wrong source: %+v", pkgs)
	}
}

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
	if len(updated.PackagesFor("windows-x64")) != len(prof.ComponentsFor("windows-x64")) || len(updated.PackagesFor("linux-x64")) != len(old) {
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
