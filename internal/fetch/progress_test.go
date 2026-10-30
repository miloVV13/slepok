package fetch

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressLineAndResumedSpeed(t *testing.T) {
	start := time.Unix(100, 0)
	p := &progressWriter{name: "arm-gcc", initial: 50 << 20, done: 75 << 20, total: 100 << 20, started: start}
	line := p.line(start.Add(5*time.Second), 99, false)
	for _, want := range []string{"arm-gcc", "[", "75%", "75.0/100.0 MiB", "5.0 MiB/s", "ETA 5s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q: %q", want, line)
		}
	}
	for _, width := range []int{1, 10, 35, 60, 80} {
		if line := p.line(start.Add(time.Second), width, false); len([]rune(line)) > width {
			t.Fatalf("line exceeds %d columns: %q", width, line)
		}
	}
	p.total = -1
	line = p.line(start.Add(5*time.Second), 79, false)
	if strings.Contains(line, "%") || strings.Contains(line, "ETA") || !strings.Contains(line, "75.0 MiB") {
		t.Fatal(line)
	}
}

func TestInteractiveProgressReusesLineAndEndsBeforeOtherOutput(t *testing.T) {
	var buf bytes.Buffer
	f := New(nil, &buf)
	start := time.Now().Add(-time.Second)
	p := &progressWriter{f: f, name: "python", done: 1 << 20, total: 10 << 20, started: start, interactive: true, width: 80}
	p.draw(time.Now(), false)
	p.done = 10 << 20
	p.finish(true)
	f.logf("  extracting python\n")
	output := buf.String()
	if strings.Count(output, "\n") != 2 || strings.Count(output, "\r") != 2 || !strings.Contains(output, "100%") || !strings.HasSuffix(output, "\n  extracting python\n") {
		t.Fatalf("bad output: %q", output)
	}
	buf.Reset()
	p.done = 1 << 20
	p.finish(false)
	f.logf("retrying\n")
	if !strings.Contains(buf.String(), "stopped") || !strings.HasSuffix(buf.String(), "\nretrying\n") {
		t.Fatalf("bad failed transfer: %q", buf.String())
	}
}

func TestRedirectedProgressContainsNoTerminalControls(t *testing.T) {
	var buf bytes.Buffer
	f := New(nil, &buf)
	p := newProgress(f, "ninja", 0, 4)
	if p.interactive {
		t.Fatal("buffer detected as terminal")
	}
	_, err := p.Write([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	p.finish(true)
	if strings.ContainsAny(buf.String(), "\r\x1b") || strings.Count(buf.String(), "\n") != 1 || !strings.Contains(buf.String(), "100%") {
		t.Fatalf("bad log: %q", buf.String())
	}
}
