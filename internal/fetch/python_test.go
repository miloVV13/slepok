package fetch

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloVV13/slepok/internal/profile"
)

func pythonWheel(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		w, err := z.Create(name)
		requireOK(t, err)
		_, err = io.WriteString(w, data)
		requireOK(t, err)
	}
	requireOK(t, z.Close())
	return b.Bytes()
}

func TestPythonBundlePortableAndNativeAgree(t *testing.T) {
	wheel := pythonWheel(t, map[string]string{
		"demo/__init__.py": "value = 42\n", "demo-1.dist-info/METADATA": "Name: demo\nVersion: 1\n",
		"demo-1.dist-info/licenses/LICENSE": "license", "demo-1.data/purelib/extra.py": "extra = 1\n",
		"demo-1.data/scripts/demo": "#!/old/path/python", "demo.pth": "# pinned site processing\n",
	})
	sdist := tarGz(t, entry{name: "source/pkg/__init__.py", typ: tar.TypeReg, body: "pure Python"}, entry{name: "source/setup.py", typ: tar.TypeReg, body: "raise RuntimeError('must never run')"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wheel" {
			w.Write(wheel)
		} else {
			w.Write(sdist)
		}
	}))
	defer srv.Close()
	src := profile.Source{Python: []profile.PythonArchive{
		{URL: srv.URL + "/wheel", SHA256: sha(wheel), Format: "wheel"},
		{URL: srv.URL + "/source", SHA256: sha(sdist), Format: "tar.gz", Strip: 1, Include: []string{"pkg"}},
	}}
	f := newFetcher(t)
	digest, err := f.Install("python-libs", src)
	requireOK(t, err)
	dir, err := f.Store.Path(digest)
	requireOK(t, err)
	for _, file := range []string{"demo/__init__.py", "demo-1.dist-info/licenses/LICENSE", "extra.py", "pkg/__init__.py", "sitecustomize.py", "demo.pth"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
			t.Fatal(file, err)
		}
	}
	for _, file := range []string{"setup.py", "demo-1.data/scripts/demo"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); !os.IsNotExist(err) {
			t.Fatal("unexpected installed file", file, err)
		}
	}
	src.Digest = digest
	portable := newFetcher(t)
	got, err := portable.InstallPortable("python-libs", src)
	requireOK(t, err)
	if got != digest || portable.Store.HasTree(got) {
		t.Fatal("portable bundle differs or was extracted")
	}
	// A restored package uses its own library root, not a build-time path.
	data, err := os.ReadFile(filepath.Join(dir, "sitecustomize.py"))
	requireOK(t, err)
	if !strings.Contains(string(data), "__file__") || strings.Contains(string(data), dir) {
		t.Fatal("nonrelocatable Python startup")
	}
}

func TestPythonBundleRejectsUnsupportedOrConflictingFiles(t *testing.T) {
	for _, files := range []map[string]string{
		{"demo-1.data/headers/header.h": "x"},
		{"demo.py": "a", "demo-1.data/purelib/demo.py": "b"},
		{"../outside": "x"},
		{"sitecustomize.py": "override controlled startup"},
	} {
		data := pythonWheel(t, files)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
		f := newFetcher(t)
		_, err := f.Install("libs", profile.Source{Python: []profile.PythonArchive{{URL: srv.URL, SHA256: sha(data), Format: "wheel"}}})
		srv.Close()
		if err == nil {
			t.Fatal("unsupported bundle accepted", files)
		}
	}
}
