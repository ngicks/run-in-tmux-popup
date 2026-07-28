package cmdsignals

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"
)

const testTimeout = 2 * time.Second

// newMgr builds a signalChanMgr wired to a fresh cancel-cause context, without
// going through signal.Notify so signals can be injected deterministically.
func newMgr(t *testing.T) (*signalChanMgr, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	return &signalChanMgr{
		c:      make(chan os.Signal, 1),
		ctx:    ctx,
		cancel: cancel,
	}, ctx
}

func mgrContext(ctx context.Context, mgr *signalChanMgr) context.Context {
	return context.WithValue(ctx, signalChanMgrKey, mgr)
}

// runBlockOn starts blockOn in a goroutine and returns a channel closed when it
// returns.
func runBlockOn(mgr *signalChanMgr) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		mgr.blockOn()
		close(done)
	}()
	return done
}

func mustReturn(t *testing.T, done <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal(msg)
	}
}

// waitFor polls cond until it is true or testTimeout elapses.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBlockOn_SignalCancelsContextWithCause(t *testing.T) {
	mgr, ctx := newMgr(t)

	done := runBlockOn(mgr)
	mgr.c <- syscall.SIGTERM
	mustReturn(t, done, "blockOn did not return after a signal")

	if ctx.Err() == nil {
		t.Fatal("context should be cancelled after a signal")
	}
	want := fmt.Sprintf("signal received: %q", syscall.SIGTERM)
	if got := context.Cause(ctx); got == nil || got.Error() != want {
		t.Fatalf("cause = %v, want %q", got, want)
	}
}

func TestBlockOn_ParentCancelReturnsWithoutOverridingCause(t *testing.T) {
	mgr, ctx := newMgr(t)

	done := runBlockOn(mgr)
	want := errors.New("parent cancelled")
	mgr.cancel(want) // simulate the caller's own cancel(err)
	mustReturn(t, done, "blockOn did not return after the context was cancelled")

	// blockOn took the ctx.Done() branch, so it must not have replaced the cause
	// with a "signal received" error.
	if got := context.Cause(ctx); got != want {
		t.Fatalf("cause = %v, want %v", got, want)
	}
}

func TestPause_InstallsHandlerStopsAndDrains(t *testing.T) {
	mgr, ctx := newMgr(t)
	ctx = mgrContext(ctx, mgr)

	mgr.c <- syscall.SIGTERM
	installed := false
	if !Pause(ctx, func() {
		installed = true
		if mgr.paused {
			t.Fatal("handler installed after pause flag was set")
		}
	}) {
		t.Fatal("Pause returned false")
	}

	if !installed {
		t.Fatal("installHandler was not called")
	}
	if !mgr.paused {
		t.Fatal("manager should be paused")
	}
	if len(mgr.c) != 0 {
		t.Fatal("Pause did not drain queued signals")
	}
	if Pause(ctx, nil) {
		t.Fatal("second Pause returned true")
	}
}

func TestResume_NotifiesBeforeRemovingHandler(t *testing.T) {
	mgr, ctx := newMgr(t)
	ctx = mgrContext(ctx, mgr)
	mgr.paused = true
	t.Cleanup(func() { signal.Stop(mgr.c) })

	removed := false
	if !Resume(ctx, func() {
		removed = true
		if mgr.paused {
			t.Fatal("handler removed before pause flag was cleared")
		}
	}) {
		t.Fatal("Resume returned false")
	}

	if !removed {
		t.Fatal("removeHandler was not called")
	}
	if mgr.paused {
		t.Fatal("manager should be resumed")
	}
	if Resume(ctx, nil) {
		t.Fatal("second Resume returned true")
	}
}

func TestPauseResume_ReturnFalseForInvalidContext(t *testing.T) {
	if Pause(context.Background(), nil) {
		t.Fatal("Pause returned true for a context without a signal manager")
	}
	if Resume(context.Background(), nil) {
		t.Fatal("Resume returned true for a context without a signal manager")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Pause(ctx, nil) {
		t.Fatal("Pause returned true for a cancelled context")
	}
	if Resume(ctx, nil) {
		t.Fatal("Resume returned true for a cancelled context")
	}
}

// TestPauseResume_BlockOnObeysPauseState drives a running blockOn through a
// pause/resume cycle: while paused a delivered signal must be discarded and the
// context left alive (exercising the `if mgr.paused { continue }` branch), and
// after resume the next signal must cancel.
func TestPauseResume_BlockOnObeysPauseState(t *testing.T) {
	mgr, ctx := newMgr(t)
	ctx = mgrContext(ctx, mgr)
	// Resume re-registers mgr.c via signal.Notify; make sure it is unregistered
	// even if the test fails before blockOn returns.
	t.Cleanup(func() { signal.Stop(mgr.c) })

	done := runBlockOn(mgr)

	// Paused: blockOn must consume the signal but not cancel.
	if !Pause(ctx, nil) {
		t.Fatal("Pause returned false")
	}
	mgr.c <- syscall.SIGTERM
	waitFor(t, func() bool { return len(mgr.c) == 0 },
		"blockOn did not consume the signal delivered while paused")
	if ctx.Err() != nil {
		t.Fatalf("context was cancelled while paused: %v", context.Cause(ctx))
	}
	select {
	case <-done:
		t.Fatal("blockOn returned while paused instead of ignoring the signal")
	default:
	}

	// Resumed: the next signal must cancel with the signal cause.
	if !Resume(ctx, nil) {
		t.Fatal("Resume returned false")
	}
	mgr.c <- syscall.SIGTERM
	mustReturn(t, done, "blockOn did not return after a signal once resumed")

	if ctx.Err() == nil {
		t.Fatal("context should be cancelled after a signal once resumed")
	}
	want := fmt.Sprintf("signal received: %q", syscall.SIGTERM)
	if got := context.Cause(ctx); got == nil || got.Error() != want {
		t.Fatalf("cause = %v, want %q", got, want)
	}
}

func TestBlockOn_OnlyRunsOnce(t *testing.T) {
	mgr, _ := newMgr(t)

	// Pre-cancel so the first call returns immediately and latches blocked.
	mgr.cancel(errors.New("init done"))
	mgr.blockOn()

	// A buffered signal that a second (incorrect) run would consume.
	mgr.c <- syscall.SIGTERM

	mustReturn(t, runBlockOn(mgr), "second blockOn did not return promptly")

	// The guard must short-circuit before the select, leaving the signal in the
	// buffer.
	if len(mgr.c) != 1 {
		t.Fatal(
			"second blockOn consumed the buffered signal; the blocked guard did not short-circuit",
		)
	}
}

func TestNotifyContext_PanicsOnCancelledContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	defer func() {
		if recover() == nil {
			t.Fatal("expected NotifyContext to panic on an already-cancelled context")
		}
	}()
	NotifyContext(parent)
}

func TestNotifyContext_PanicsOnNilContext(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected NotifyContext to panic on a nil context")
		}
	}()
	// A nil Context is held in a variable (rather than passed as a literal) to
	// exercise the documented "panic also when ctx is nil" path.
	var nilCtx context.Context
	NotifyContext(nilCtx)
}

func TestNotifyContext_CancelPropagatesCause(t *testing.T) {
	blockOn, ctx, cancel := NotifyContext(context.Background())
	if blockOn == nil || ctx == nil || cancel == nil {
		t.Fatal("NotifyContext returned a nil value")
	}

	// Drive blockOn to completion so its deferred signal.Stop runs and the
	// process-global signal registration does not leak into other tests.
	var wg sync.WaitGroup
	wg.Go(blockOn)
	t.Cleanup(wg.Wait)

	want := errors.New("shutdown")
	cancel(want)
	<-ctx.Done()

	if got := context.Cause(ctx); got != want {
		t.Fatalf("cause = %v, want %v", got, want)
	}
}
