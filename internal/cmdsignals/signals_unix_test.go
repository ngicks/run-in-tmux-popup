//go:build unix

package cmdsignals

import (
	"context"
	"fmt"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestNotifyContext_DeliversRealSignal exercises the full signal.Notify wiring
// by sending the process a real SIGTERM and asserting the returned context is
// cancelled with the matching cause.
func TestNotifyContext_DeliversRealSignal(t *testing.T) {
	blockOn, ctx, cancel := NotifyContext(context.Background())
	defer cancel(nil)

	var wg sync.WaitGroup
	wg.Go(blockOn)
	t.Cleanup(wg.Wait)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context was not cancelled after SIGTERM")
	}

	want := fmt.Sprintf("signal received: %q", syscall.SIGTERM)
	if got := context.Cause(ctx); got == nil || got.Error() != want {
		t.Fatalf("cause = %v, want %q", got, want)
	}
}
