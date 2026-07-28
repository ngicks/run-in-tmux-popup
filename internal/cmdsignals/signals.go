// Package cmdsignals lists the OS signals that should cancel top-level CLI execution.
package cmdsignals

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// ExitSignals are the signals that should cancel top-level CLI execution.
var ExitSignals = [...]os.Signal{
	os.Interrupt,
	syscall.SIGTERM,
}

type SignalReceivedError struct {
	Sig os.Signal
}

func (e *SignalReceivedError) Error() string {
	return fmt.Sprintf("signal received: %q", e.Sig)
}

var signalChanMgrKey = new("signalChanMgr")

type signalChanMgr struct {
	mu      sync.Mutex
	blocked bool
	c       chan os.Signal
	ctx     context.Context
	cancel  context.CancelCauseFunc
	paused  bool
}

func (mgr *signalChanMgr) blockOn() {
	mgr.mu.Lock()
	if mgr.blocked {
		mgr.mu.Unlock()
		return
	}
	mgr.blocked = true
	mgr.mu.Unlock()

	defer signal.Stop(mgr.c)

	for {
		select {
		case <-mgr.ctx.Done():
		case sig := <-mgr.c:
			mgr.mu.Lock()
			if mgr.paused {
				mgr.mu.Unlock()
				continue
			}
			mgr.mu.Unlock()
			mgr.cancel(&SignalReceivedError{Sig: sig})
		}
		return
	}
}

// NotifyContext wires up [ExitSignals] to cancel ctx when those signals are received.
// blockOn must be called to make signal propagation work.
//
// The ctx will be cancelled with [*SignalReceivedError] when those listed in [ExitSignals]
// received.
// Callers are advised to check errors with [context.Cause].
//
//	err := work(ctx)
//	if err != nil {
//		if errors.Is(err, ctx.Err()) {
//			if sigErr, ok := errors.AsType[*SignalReceivedError](context.Cause(ctx));  ok {
//				// log as signal cancellation using sigErr.Sig
//				// or print nothing and exit as if normal exit
//				return
//			}
//		}
//		// non-cancellation error.
//	}
//
// As name suggests, blockOn blocks a calling goroutine.
// So callers are advised to call it with `go` keyword (i.e. `go blockOn`)
// or with [sync.WaitGroup.Go].
// Callers should call cancel in every code path if they wish to clean up resources.
//
// [Pause] and [Resume] might be used to temporarily disable / re-enable
// cancellation. This is useful when forwarding signals to another process,
// i.e. attaching to other terminal apps.
func NotifyContext(
	inCtx context.Context,
) (blockOn func(), ctx context.Context, cancel func(error)) {
	if inCtx.Err() != nil { // panic also when ctx is nil
		panic("ctx is already cancelled")
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, ExitSignals[:]...)

	ctx, cancel = context.WithCancelCause(inCtx)

	mgr := &signalChanMgr{
		c:      c,
		cancel: cancel,
	}

	mgr.ctx = context.WithValue(ctx, signalChanMgrKey, mgr)

	return mgr.blockOn, mgr.ctx, mgr.cancel
}

// Pause temporarily unregisters this package's signal handler.
//
// installHandler is called while Pause holds its internal lock, before this
// package calls signal.Stop on its own channel. Callers must use installHandler
// to install their own handler for [ExitSignals] if the process should not fall
// back to the default SIGINT / SIGTERM behavior while paused. installHandler
// must not call Pause or [Resume].
//
// Pause drains signals already queued for this package's handler before
// returning.
func Pause(ctx context.Context, installHandler func()) (ok bool) {
	if ctx.Err() != nil {
		return false
	}

	mgr, ok := ctx.Value(signalChanMgrKey).(*signalChanMgr)
	if !ok {
		return false
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.paused {
		return false
	}
	if installHandler != nil {
		installHandler()
	}
	mgr.paused = true
	signal.Stop(mgr.c)
	for {
		select {
		case <-mgr.c:
		default:
			return true
		}
	}
}

// Resume re-registers this package's signal handler.
//
// removeHandler is called while Resume holds its internal lock, after this
// package has called signal.Notify on its own channel. Callers should use
// removeHandler to uninstall the handler installed by [Pause]. removeHandler
// must not call [Pause] or Resume.
func Resume(ctx context.Context, removeHandler func()) (ok bool) {
	if ctx.Err() != nil {
		return false
	}

	mgr, ok := ctx.Value(signalChanMgrKey).(*signalChanMgr)
	if !ok {
		return false
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if !mgr.paused {
		return false
	}
	signal.Notify(mgr.c, ExitSignals[:]...)
	mgr.paused = false
	if removeHandler != nil {
		removeHandler()
	}
	return true
}
