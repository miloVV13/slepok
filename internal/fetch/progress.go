package fetch

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// Progress uses carriage returns only on an actual terminal. Logs keep sparse,
// complete lines, and redirected output never contains terminal controls.
type progressWriter struct {
	f                      *Fetcher
	name                   string
	done, total, initial   int64
	started, last          time.Time
	interactive            bool
	width, previous, frame int
}

func newProgress(f *Fetcher, name string, offset, total int64) *progressWriter {
	now := time.Now()
	width, interactive := terminalWidth(f.Log)
	p := &progressWriter{f: f, name: strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name), done: offset, total: total, initial: offset, started: now, last: now, interactive: interactive, width: width}
	if interactive {
		p.draw(now, false)
	}
	return p
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	every := p.f.ProgressEvery
	if p.interactive {
		every = 150 * time.Millisecond
	}
	now := time.Now()
	if now.Sub(p.last) >= every {
		p.draw(now, false)
		p.last = now
	}
	return len(b), nil
}

func (p *progressWriter) finish(success bool) {
	if p.interactive {
		p.draw(time.Now(), !success)
		p.f.logf("\n")
	} else if success {
		p.draw(time.Now(), false)
	}
}

func (p *progressWriter) draw(now time.Time, stopped bool) {
	if !p.interactive {
		if p.total > 0 {
			p.f.logf("  %s: %.1f of %.1f MiB (%d%%)\n", p.name, mib(p.done), mib(p.total), percentage(p.done, p.total))
		} else {
			p.f.logf("  %s: %.1f MiB\n", p.name, mib(p.done))
		}
		return
	}
	if width, ok := terminalWidth(p.f.Log); ok {
		p.width = width
	}
	line := p.line(now, p.width-1, stopped)
	// Pad shorter updates so old characters disappear without requiring ANSI
	// support (including classic Windows consoles).
	clear := min(p.previous, max(0, p.width-1)) - len([]rune(line))
	if clear < 0 {
		clear = 0
	}
	p.f.logf("\r%s%s", line, strings.Repeat(" ", clear))
	p.previous = len([]rune(line))
	p.frame++
}

func (p *progressWriter) line(now time.Time, width int, stopped bool) string {
	name := clipProgress(p.name, 14)
	speed := float64(0)
	if elapsed := now.Sub(p.started).Seconds(); elapsed > 0 {
		speed = float64(p.done-p.initial) / elapsed
	}
	rate := fmt.Sprintf("%.1f KiB/s", speed/1024)
	if speed >= 1<<20 {
		rate = fmt.Sprintf("%.1f MiB/s", speed/(1<<20))
	}
	suffix := fmt.Sprintf("%.1f MiB  %s", mib(p.done), rate)
	prefix := "  " + name
	if p.total > 0 {
		suffix = fmt.Sprintf("%3d%%  %.1f/%.1f MiB  %s", percentage(p.done, p.total), mib(p.done), mib(p.total), rate)
		if !stopped && speed > 0 && p.done < p.total {
			seconds := int64(math.Ceil(float64(p.total-p.done) / speed))
			eta := fmt.Sprintf("%ds", seconds)
			if seconds >= 60 {
				eta = fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
			}
			if len(prefix)+len(suffix)+len(eta)+16 <= width {
				suffix += "  ETA " + eta
			}
		}
		barWidth := min(24, width-len([]rune(prefix))-len(suffix)-5)
		if barWidth >= 8 {
			filled := int(float64(barWidth) * min(1, float64(p.done)/float64(p.total)))
			bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
			prefix += " [" + bar + "]"
		}
	} else {
		prefix += " " + string("|/-\\"[p.frame%4])
	}
	if stopped {
		suffix += " (stopped)"
	}
	return clipProgress(prefix+"  "+suffix, width)
}

func percentage(done, total int64) int {
	return int(min(100, max(0, float64(done)/float64(total)*100)))
}

func clipProgress(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}
