// Package process starts cancellable, non-interactive build and git commands.
package process

import (
	"context"
	"github.com/miloVV13/slepok/internal/contextio"
	"os/exec"
	"time"
)

func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(contextio.OrBackground(ctx), name, args...)
	cmd.WaitDelay = 2 * time.Second
	configure(cmd)
	return cmd
}

// InteractiveCommand keeps a terminal reader in the foreground process group.
// Non-interactive commands use a separate group for cancellation; on Unix that
// group cannot read the controlling terminal (SIGTTIN).
func InteractiveCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(contextio.OrBackground(ctx), name, args...)
	cmd.WaitDelay = 2 * time.Second
	configureInteractive(cmd)
	return cmd
}
