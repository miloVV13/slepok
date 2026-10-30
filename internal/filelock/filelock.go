// Package filelock provides cancellable process locks. Lock files are never
// removed: unlinking one while another process holds it would split the lock.
package filelock

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/miloVV13/slepok/internal/contextio"
)

// Acquire returns an open file whose Close releases the lock. Shared holders
// may coexist; an exclusive holder excludes all others. The OS also releases
// locks on process exit, including SIGKILL.
func Acquire(ctx context.Context, path string, shared bool) (*os.File, error) {
	ctx = contextio.OrBackground(ctx)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		ok, err := try(f, shared)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if ok {
			return f, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
