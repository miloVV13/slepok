package fetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/miloVV13/slepok/internal/profile"
	"github.com/miloVV13/slepok/internal/store"
)

func TestRealSTM32SDKPackages(t *testing.T) {
	root := os.Getenv("SLEPOK_STM32_PACKAGES")
	if root == "" {
		t.Skip("SLEPOK_STM32_PACKAGES not set")
	}
	st, err := store.Open(root)
	requireOK(t, err)
	guard, err := st.Acquire(t.Context(), false)
	requireOK(t, err)
	defer guard.Close()
	p, err := profile.Load("stm32f103c8")
	requireOK(t, err)
	f := New(st, os.Stdout)
	f.Context = t.Context()
	files := map[string]string{
		"cmsis-core":     "CMSIS/Core/Include/core_cm3.h",
		"stm32f1-device": "Source/Templates/gcc/startup_stm32f103xb.s",
		"stm32f1-hal":    "Inc/stm32f1xx_hal.h",
	}
	digests := map[string]string{}
	for _, c := range p.Components {
		file, ok := files[c.Name]
		if !ok {
			continue
		}
		for _, platform := range []string{"linux-x64", "windows-x64"} {
			src, recorded, err := c.Source(platform)
			requireOK(t, err)
			digest, err := f.Install(c.Name, src)
			requireOK(t, err)
			dir, err := st.Path(digest)
			requireOK(t, err)
			_, err = os.Stat(filepath.Join(dir, filepath.FromSlash(file)))
			requireOK(t, err)
			digests[c.Name+"/"+recorded] = digest
			t.Logf("%s/%s = %s", c.Name, recorded, digest)
		}
	}
	data, err := json.MarshalIndent(digests, "", "  ")
	requireOK(t, err)
	requireOK(t, os.WriteFile(filepath.Join(root, "stm32-digests.json"), data, 0644))
}
