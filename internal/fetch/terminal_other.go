//go:build !linux && !windows

package fetch

import "io"

func terminalWidth(io.Writer) (int, bool) { return 0, false }
