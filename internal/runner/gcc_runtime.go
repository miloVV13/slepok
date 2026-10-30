package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/miloVV13/slepok/internal/store"
)

// Intel Arm GCC 14.2 loads Homebrew's libzstd by an absolute install name.
// A shell launcher sets the packaged library path after macOS has entered
// /bin/sh (SIP removes inherited DYLD_* when entering system executables).
// Vendor Mach-O files and their signatures remain unchanged.
// The derived launchers are reproducible for a given store, not snapshot data;
// Resolve recreates them after GC, before CMake uses its cached compiler path.
func gccRuntimeLaunchers(ctx context.Context, st *store.Store, gcc, libraries string) (string, error) {
	work, err := st.TempDir("gcc-runtime-")
	if err != nil {
		return "", err
	}
	defer store.RemoveTree(work)
	bin := filepath.Join(work, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(gcc, "bin"))
	if err != nil {
		return "", err
	}
	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		tool := filepath.Join(gcc, "bin", entry.Name())
		info, err := os.Stat(tool)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		data := "#!/bin/sh\nexport DYLD_LIBRARY_PATH=" + shellQuote(libraries) + "\nexec " + shellQuote(tool) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, entry.Name()), []byte(data), 0755); err != nil {
			return "", err
		}
		count++
	}
	if count == 0 {
		return "", fmt.Errorf("GCC runtime has no executable tools")
	}
	digest, err := st.InstallDirContext(ctx, work, "")
	if err != nil {
		return "", err
	}
	return st.Path(digest)
}
