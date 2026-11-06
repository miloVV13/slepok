package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/miloVV13/slepok/internal/fetch"
	"github.com/miloVV13/slepok/internal/manifest"
	"github.com/miloVV13/slepok/internal/process"
	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/project"
	"github.com/miloVV13/slepok/internal/store"
)

// pip belongs to the locked Python runtime. It resolves wheels only, without
// installing or executing build backends. The receiver never invokes pip.
const resolvePython = `
import json, sys
from pip._vendor.packaging.markers import Marker
target, report, constraints, *requirements = sys.argv[1:]
systems = {
 'linux-x64': ('linux', 'Linux', 'posix', 'x86_64'),
 'windows-x64': ('win32', 'Windows', 'nt', 'AMD64'),
 'macos-arm64': ('darwin', 'Darwin', 'posix', 'arm64'),
 'macos-x64': ('darwin', 'Darwin', 'posix', 'x86_64'),
}
platform, system, osname, machine = systems[target]
evaluate = Marker.evaluate
def target_evaluate(self, environment=None, **kwargs):
    environment = dict(environment or {})
    environment.update(sys_platform=platform, platform_system=system,
                       os_name=osname, platform_machine=machine)
    # An unknown target OS release cannot be resolved honestly on this host.
    if any(key in str(self) for key in ('platform_release', 'platform_version')):
        raise RuntimeError('OS-release-specific Python dependency markers are unsupported')
    return evaluate(self, environment, **kwargs)
Marker.evaluate = target_evaluate
from urllib.parse import urlsplit
from pip._internal.operations.prepare import RequirementPreparer
prepare = RequirementPreparer.prepare_linked_requirement
def wheel_only(self, req, *args, **kwargs):
    link = req.link
    if (link is None or not link.is_wheel or
        urlsplit(link.url).scheme != 'https' or
        urlsplit(link.url).hostname != 'files.pythonhosted.org'):
        raise RuntimeError('Only PyPI wheel artifacts are supported; source builds are disabled')
    return prepare(self, req, *args, **kwargs)
RequirementPreparer.prepare_linked_requirement = wheel_only
from pip._internal.cli.main import main
args = ['install', '--dry-run', '--ignore-installed', '--no-cache-dir',
        '--disable-pip-version-check', '--only-binary=:all:', '--report', report,
        '--index-url', 'https://pypi.org/simple', '--constraint', constraints, '--implementation', 'cp',
        '--python-version', '.'.join(map(str, sys.version_info[:3])),
        '--abi', 'cp%d%d' % sys.version_info[:2]]
tags = {
 'windows-x64': ['win_amd64'],
 'macos-arm64': ['macosx_15_0_arm64'],
 'macos-x64': ['macosx_15_0_x86_64'],
 'linux-x64': ['manylinux_2_%d_x86_64' % n for n in range(34, 4, -1)] +
              ['manylinux2014_x86_64', 'manylinux2010_x86_64', 'manylinux1_x86_64'],
}
for tag in tags[target]: args += ['--platform', tag]
raise SystemExit(main(args + requirements))
`

type pythonReportItem struct {
	Download struct {
		URL     string `json:"url"`
		Archive struct {
			Hashes map[string]string `json:"hashes"`
		} `json:"archive_info"`
	} `json:"download_info"`
	Metadata struct{ Name, Version, License string } `json:"metadata"`
}

type pythonReport struct {
	Version string             `json:"version"`
	Install []pythonReportItem `json:"install"`
}

var resolvedPythonIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*==[A-Za-z0-9][A-Za-z0-9.!+_-]*$`)

func pythonSource(data []byte) (profile.Source, error) {
	var report pythonReport
	if err := json.Unmarshal(data, &report); err != nil {
		return profile.Source{}, err
	}
	if report.Version != "1" || len(report.Install) == 0 {
		return profile.Source{}, fmt.Errorf("unsupported or empty pip report")
	}
	src := profile.Source{}
	slices.SortFunc(report.Install, func(a, b pythonReportItem) int {
		return strings.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	for _, item := range report.Install {
		identity := item.Metadata.Name + "==" + item.Metadata.Version
		if !resolvedPythonIdentity.MatchString(identity) {
			return src, fmt.Errorf("invalid resolved Python distribution identity %q", identity)
		}
		u, err := url.Parse(item.Download.URL)
		hash := item.Download.Archive.Hashes["sha256"]
		_, hashErr := hex.DecodeString(hash)
		if err != nil || u.Scheme != "https" || u.Host != "files.pythonhosted.org" || !strings.HasSuffix(u.Path, ".whl") || len(hash) != 64 || hashErr != nil {
			return src, fmt.Errorf("resolver returned an unpinned wheel for %s", item.Metadata.Name)
		}
		src.Python = append(src.Python, profile.PythonArchive{Name: item.Metadata.Name, Version: item.Metadata.Version, URL: item.Download.URL, SHA256: hash, Format: "wheel"})
	}
	return src, nil
}

func prepareProjectPython(ctx context.Context, cfg project.Config, lock *project.Lock, st *store.Store, f *fetch.Fetcher) error {
	requirements := cfg.Python.Requirements
	if !slices.Equal(requirements, lock.PythonRequirements) || len(requirements) == 0 {
		lock.Packages = slices.DeleteFunc(lock.Packages, func(p project.LockedPackage) bool { return p.Name == "project-python" })
	}
	if len(requirements) == 0 {
		lock.PythonRequirements = nil
		return nil
	}
	var python, version string
	for _, p := range lock.PackagesFor(manifest.HostPlatform()) {
		if p.Name == "python" {
			python, _ = st.Path(p.Source.Digest)
			version = p.Version
		}
	}
	if python == "" {
		return fmt.Errorf("project Python dependencies require a locked Python runtime")
	}
	if runtime.GOOS == "windows" {
		python = filepath.Join(python, "python.exe")
	} else {
		python = filepath.Join(python, "bin", "python3")
	}
	for _, platform := range cfg.Platforms {
		found := false
		for _, p := range lock.PackagesFor(platform) {
			if p.Name == "project-python" {
				found = true
			}
			if p.Name == "python" && p.Version != version {
				return fmt.Errorf("Python runtime versions differ across targets")
			}
		}
		if found {
			continue
		}
		work, err := st.TempDir("python-resolve-")
		if err != nil {
			return err
		}
		report := filepath.Join(work, "report.json")
		constraints := filepath.Join(work, "constraints.txt")
		versions := map[string]string{}
		for _, pkg := range lock.Packages {
			if pkg.Name != "project-python" {
				continue
			}
			for _, a := range pkg.Source.Python {
				if a.Name == "" || a.Version == "" {
					continue
				}
				name := strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(a.Name))
				if previous := versions[name]; previous != "" && previous != a.Version {
					store.RemoveTree(work)
					return fmt.Errorf("conflicting locked Python versions for %s", name)
				}
				versions[name] = a.Version
			}
		}
		lines := []string{}
		for name, version := range versions {
			lines = append(lines, name+"=="+version)
		}
		slices.Sort(lines)
		if err := os.WriteFile(constraints, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			store.RemoveTree(work)
			return err
		}
		fmt.Printf("Resolving project Python wheels for %s...\n", platform)
		args := append([]string{"-I", "-c", resolvePython, platform, report, constraints}, requirements...)
		cmd := process.Command(ctx, python, args...)
		cmd.Env = []string{}
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			upper := strings.ToUpper(key)
			if !strings.HasPrefix(upper, "PIP_") && !strings.HasPrefix(upper, "PYTHON") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PIP_CONFIG_FILE="+os.DevNull)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		err = cmd.Run()
		if err != nil {
			store.RemoveTree(work)
			return fmt.Errorf("resolve Python wheels for %s: %w", platform, err)
		}
		data, err := os.ReadFile(report)
		store.RemoveTree(work)
		if err != nil {
			return err
		}
		src, err := pythonSource(data)
		if err != nil {
			return err
		}
		var digest string
		if platform == manifest.HostPlatform() {
			digest, err = f.Install("project-python", src)
		} else {
			digest, err = f.InstallPortable("project-python", src)
		}
		if err != nil {
			return err
		}
		src.Digest = digest
		lock.Packages = append(lock.Packages, project.LockedPackage{Name: "project-python", Version: "1", Platform: platform, License: "See wheel .dist-info license files", Source: src})
	}
	lock.PythonRequirements = slices.Clone(requirements)
	return nil
}
