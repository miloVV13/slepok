package safepath

import (
	"runtime"
	"testing"
)

func TestCheckWindows(t *testing.T) {
	valid := []string{"src/main.c", "folder with spaces/readme.txt", "auxiliary.c", "COM10.txt", ".hidden", "conifer.txt"}
	invalid := []string{"C:/outside", "C:relative", "file:stream", "CON", "nul.txt", "dir/PRN.c", "AUX .txt", "COM1.log", "com¹.txt", "LPT³", "CONIN$", "conout$", "name.", "name ", "dir./file", "a?b", "a*b", `a"b`, "a<b", "a>b", "a|b", "../outside", "a\\b"}
	for _, p := range valid {
		if err := CheckWindows(p); err != nil {
			t.Errorf("CheckWindows(%q) = %v", p, err)
		}
	}
	for _, p := range invalid {
		if err := CheckWindows(p); err == nil {
			t.Errorf("CheckWindows(%q) accepted an unsafe path", p)
		}
	}
}

func TestHostSpecificNames(t *testing.T) {
	for _, p := range []string{"CON", "file:stream", "trailing."} {
		if err := Check(p); err != nil {
			t.Errorf("portable Check(%q) depends on host: %v", p, err)
		}
		err := CheckForHost(p)
		if (err != nil) != (runtime.GOOS == "windows") {
			t.Errorf("Check(%q) = %v on %s", p, err, runtime.GOOS)
		}
	}
}

func TestHostSpecificLinkTargets(t *testing.T) {
	if err := CheckLinkTarget("bin/tool", "../CON"); err != nil {
		t.Fatal(err)
	}
	err := CheckLinkTargetForHost("bin/tool", "../CON")
	if (err != nil) != (runtime.GOOS == "windows") {
		t.Fatalf("host link target check = %v on %s", err, runtime.GOOS)
	}
	if err := CheckLinkTargetForHost("dir/link", ".."); err != nil {
		t.Fatal(err)
	}
}

func TestCheck(t *testing.T) {
	ok := []string{"a", "a/b", "a/b.c", "lib/gcc/arm-none-eabi/14.2.1/include", "a..b", ".hidden"}
	bad := []string{"", "/a", "a/", "a//b", "./a", "a/./b", "..", "../a", "a/../b", "a/..",
		"a\\b", "a\x00b", "a\nb", "a\x7fb"}
	for _, p := range ok {
		if err := Check(p); err != nil {
			t.Errorf("Check(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range bad {
		if err := Check(p); err == nil {
			t.Errorf("Check(%q) = nil, want error", p)
		}
	}
}

func TestCheckLinkTarget(t *testing.T) {
	tests := []struct {
		link, target string
		ok           bool
	}{
		{"bin/python3", "python3.12", true},
		{"a/b/c", "../d", true},
		{"a/b/c", "../../d", true},
		{"a/b/c", "../../../d", false},
		{"a", "..", false},
		{"a", ".", true},
		{"a", "/etc/passwd", false},
		{"a", "", false},
		{"a", "b\\c", false},
		{"a/b", "c/../../..", false},
	}
	for _, tt := range tests {
		err := CheckLinkTarget(tt.link, tt.target)
		if (err == nil) != tt.ok {
			t.Errorf("CheckLinkTarget(%q, %q) = %v, want ok=%v", tt.link, tt.target, err, tt.ok)
		}
	}
}
