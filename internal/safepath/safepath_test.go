package safepath

import "testing"

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
