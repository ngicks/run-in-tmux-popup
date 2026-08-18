package runinpopup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// popupTTY is the tty the fake popup announces. It never has to exist: the
// proxy only ever forwards the name.
const popupTTY = "/dev/pts/42"

// fakePinentryCommand is the argv[0] after the program name that turns this
// test binary into a stand-in for the pinentry executable. It travels in
// PinentryArgs — which callPinentry passes through untouched — rather than in
// the environment, so the dispatch cannot reach the popup payload or any other
// subprocess a test starts.
const fakePinentryCommand = "fake-pinentry"

const (
	// pinentryReadsUntilBye records the forwarded stream verbatim and exits on
	// the line that ends a real Assuan exchange. Waiting for EOF instead would
	// deadlock: os/exec closes the child's stdin only once Wait has seen it
	// exit, and Wait is what the proxy is blocked on.
	pinentryReadsUntilBye = "read-until-bye"
	// pinentryHangs never exits on its own, so only cancellation can end it.
	pinentryHangs = "hang"
)

// runFakePinentry answers the argv newPinentryProxy builds:
//
//	<bin> fake-pinentry <transcript> <pidfile> <mode>
//
// It reports whether it took over the process, so TestMain can hand it the exit
// status instead of running the suite.
func runFakePinentry() (int, bool) {
	if len(os.Args) < 2 || os.Args[1] != fakePinentryCommand {
		return 0, false
	}
	args := os.Args[2:]
	if len(args) != 3 {
		fmt.Fprintf(os.Stderr, "unexpected fake pinentry argv: %q\n", os.Args)
		return 2, true
	}
	transcript, pidfile, mode := args[0], args[1], args[2]

	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	if mode == pinentryHangs {
		time.Sleep(time.Hour)
		return 0, true
	}

	var forwarded bytes.Buffer
	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadBytes('\n')
		forwarded.Write(line)
		if err != nil || bytes.Equal(line, []byte("BYE\n")) {
			break
		}
	}
	if err := os.WriteFile(transcript, forwarded.Bytes(), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	return 0, true
}

// ttyHandshakeBackend is shellBackend hosting the pinentry tty handshake, so
// the whole exchange can be driven without a terminal multiplexer: its popup
// payload runs in a local shell and speaks the FIFO protocol the real backends'
// payloads speak.
type ttyHandshakeBackend struct {
	shellBackend
	// script builds the popup payload from the two FIFO paths. nil announces
	// popupTTY and then waits to be dismissed, which is what every real backend
	// does.
	script       func(ttyFifo, doneFifo string) string
	validateTTY  func(line string) (string, error)
	handshakeErr error
}

func (b *ttyHandshakeBackend) NewPinentryHandshake(
	ttyFifo, doneFifo string,
) (PinentryHandshake, error) {
	if b.handshakeErr != nil {
		return PinentryHandshake{}, b.handshakeErr
	}
	script := b.script
	if script == nil {
		script = announceThenWait
	}
	return PinentryHandshake{
		Spec:        PopupSpec{Script: script(ttyFifo, doneFifo)},
		ValidateTTY: b.validateTTY,
	}, nil
}

func announceThenWait(ttyFifo, doneFifo string) string {
	return fmt.Sprintf("echo %s >> %s && read done < %s",
		shellQuote(popupTTY), shellQuote(ttyFifo), shellQuote(doneFifo))
}

// announceThenExit is a popup that goes away the moment it has reported its
// tty, the way a dismissed one does.
func announceThenExit(ttyFifo, _ string) string {
	return fmt.Sprintf("echo %s >> %s", shellQuote(popupTTY), shellQuote(ttyFifo))
}

// staysSilent is a popup that is alive but never reports anything, so only the
// tty read deadline can end the wait.
func staysSilent(_, _ string) string { return "sleep 5" }

// pinentryProxy is one wired-up exchange: a popup that touches no multiplexer,
// this test binary standing in for the pinentry executable, and pipes standing
// in for the process stdio the proxy relays between — and closes.
type pinentryProxy struct {
	dir        string
	opts       PinentryOptions
	backend    *ttyHandshakeBackend
	stdin      *os.File
	transcript string
	pidfile    string
	doneFifo   string
}

func newPinentryProxy(t *testing.T, mode string) *pinentryProxy {
	t.Helper()

	dir := t.TempDir()
	p := &pinentryProxy{
		dir:        dir,
		backend:    &ttyHandshakeBackend{shellBackend: shellBackend{stdout: new(bytes.Buffer)}},
		transcript: filepath.Join(dir, "transcript"),
		pidfile:    filepath.Join(dir, "pinentry.pid"),
		doneFifo:   filepath.Join(dir, "done"),
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the assuan stdin pipe: %v", err)
	}
	p.stdin = w
	t.Cleanup(func() { r.Close(); w.Close() })

	p.opts = PinentryOptions{
		TempDir:      dir,
		PinentryPath: os.Args[0],
		PinentryArgs: []string{fakePinentryCommand, p.transcript, p.pidfile, mode},
		// Every bound is short enough that a regression stalls one test rather
		// than the suite.
		Timeouts: TimeoutsConfig{
			Overall:   20 * time.Second,
			TTYRead:   10 * time.Second,
			DoneWrite: time.Second,
		},
		stdin:  r,
		stdout: createFile(t, dir, "pinentry-stdout"),
		stderr: createFile(t, dir, "pinentry-stderr"),
	}
	return p
}

func createFile(t *testing.T, dir, name string) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// feed queues an Assuan stream on the proxy's stdin and leaves the write end
// open, as gpg-agent does. The streams are far smaller than a pipe buffer, so
// this never blocks.
func (p *pinentryProxy) feed(t *testing.T, stream string) {
	t.Helper()
	if _, err := p.stdin.WriteString(stream); err != nil {
		t.Fatalf("feeding the assuan stream: %v", err)
	}
}

func (p *pinentryProxy) forwarded(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(p.transcript)
	if err != nil {
		t.Fatalf("reading what was forwarded to pinentry: %v", err)
	}
	return string(b)
}

func (p *pinentryProxy) pinentryPid(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(p.pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake pinentry never reported its pid")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// watchDone reads the done FIFO from outside the popup payload. The proxy opens
// that FIFO read-write and is therefore its own reader, so a second reader
// observes the dismissal without taking part in it — and gets the EOF when the
// proxy closes its end.
func watchDone(t *testing.T, path string) func() string {
	t.Helper()
	ch := make(chan string, 1)
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				break
			}
			if time.Now().After(deadline) {
				ch <- "<the done fifo was never created>"
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			ch <- fmt.Sprintf("<opening the done fifo: %v>", err)
			return
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			ch <- fmt.Sprintf("<reading the done fifo: %v>", err)
			return
		}
		ch <- string(b)
	}()
	return func() string {
		t.Helper()
		select {
		case s := <-ch:
			return s
		case <-time.After(20 * time.Second):
			t.Fatal("nothing ever reached the done fifo")
			return ""
		}
	}
}

// fdsPointingAt counts the descriptors this process still holds on path, which
// is how a FIFO the exchange forgot to close shows up.
func fdsPointingAt(t *testing.T, path string) int {
	t.Helper()
	const fdDir = "/proc/self/fd"
	ents, err := os.ReadDir(fdDir)
	if err != nil {
		t.Skipf("cannot inspect open descriptors: %v", err)
	}
	n := 0
	for _, e := range ents {
		if target, err := os.Readlink(
			filepath.Join(fdDir, e.Name()),
		); err == nil &&
			target == path {
			n++
		}
	}
	return n
}

// The one line gpg-agent sends that the proxy exists to rewrite: pinentry must
// draw in the popup, not on the terminal gpg-agent happened to pick. Everything
// around it — including the \r\n bufio.Scanner strips — is pinned byte for byte,
// since this is the exact stream the pinentry child sees.
func TestCallPinentry_rewritesTheAnnouncedTTYIntoTheStream(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.feed(t, "OPTION lc-ctype=en_US.UTF-8\n"+
		"OPTION ttyname=/dev/pts/9\n"+
		"OPTION ttytype=xterm-256color\n"+
		"OPTION grab\r\n"+
		"SETDESC Enter+passphrase\n"+
		"GETPIN\n"+
		"BYE\n")

	if err := CallPinentry(t.Context(), p.backend, p.opts); err != nil {
		t.Fatalf("CallPinentry: %v", err)
	}

	want := "OPTION lc-ctype=en_US.UTF-8\n" +
		"OPTION ttyname=" + popupTTY + "\n" +
		"OPTION ttytype=xterm-256color\n" +
		"OPTION grab\n" +
		"SETDESC Enter+passphrase\n" +
		"GETPIN\n" +
		"BYE\n"
	if got := p.forwarded(t); got != want {
		t.Errorf("forwarded to pinentry:\n%q\nwant:\n%q", got, want)
	}
}

// Only a line that starts with the option, spelled exactly that way, is
// rewritten; every near miss and everything else reaches pinentry as it came.
func TestCallPinentry_forwardsEverythingElseByteForByte(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	stream := "OPTION ttyname\n" +
		" OPTION ttyname=/dev/pts/9\n" +
		"option ttyname=/dev/pts/9\n" +
		"OPTION ttynameX=/dev/pts/9\n" +
		"SETPROMPT PIN:\n" +
		"\n" +
		"D  padded  data \n" +
		"BYE\n"
	p.feed(t, stream)

	if err := CallPinentry(t.Context(), p.backend, p.opts); err != nil {
		t.Fatalf("CallPinentry: %v", err)
	}
	if got := p.forwarded(t); got != stream {
		t.Errorf("forwarded to pinentry:\n%q\nwant it unchanged:\n%q", got, stream)
	}
}

// A popup that is dismissed the moment it has reported its tty is invisible to
// the proxy: both FIFOs are opened read-write, so neither the tty read nor the
// dismissal lacks a peer, and the exchange finishes exactly as if the popup were
// still there — dismissal bytes included.
func TestCallPinentry_popupThatGoesAwayIsNotNoticed(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = announceThenExit
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "GETPIN\nBYE\n")

	if err := CallPinentry(t.Context(), p.backend, p.opts); err != nil {
		t.Fatalf("CallPinentry: %v", err)
	}
	if got := p.forwarded(t); got != "GETPIN\nBYE\n" {
		t.Errorf("forwarded %q, want the exchange to have run to the end", got)
	}
	if got := dismissal(); got != "done\n" {
		t.Errorf("done fifo carried %q, want %q", got, "done\n")
	}
}

// pinentry never starting is reported with the path that failed — and the popup
// is still dismissed, since it would otherwise linger until the overall timeout.
func TestCallPinentry_pinentryThatCannotStart(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = announceThenExit
	p.opts.PinentryPath = filepath.Join(p.dir, "no-such-pinentry")
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "BYE\n")

	err := CallPinentry(t.Context(), p.backend, p.opts)

	if err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("err = %v, want the pinentry start failure surfaced", err)
	}
	if !strings.Contains(err.Error(), p.opts.PinentryPath) {
		t.Errorf("err = %v, want it to name %q", err, p.opts.PinentryPath)
	}
	if got := dismissal(); got != "done\n" {
		t.Errorf("done fifo carried %q, want the popup dismissed anyway", got)
	}
}

// Cancelling mid-prompt is the ordinary way a passphrase request ends: the user
// walks away and gpg-agent's deadline fires. Everything the exchange holds has
// to go with it — the pinentry process, the two handshake FIFOs, and the stdin
// it was handed, which is what unblocks the reader piping it.
func TestCallPinentry_cancellationReleasesTheProcessAndPipes(t *testing.T) {
	p := newPinentryProxy(t, pinentryHangs)
	p.feed(t, "GETPIN\n")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- CallPinentry(ctx, p.backend, p.opts) }()

	pid := p.pinentryPid(t)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("a canceled exchange must report an error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("CallPinentry never returned after the cancellation")
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("kill(%d, 0) = %v, want the pinentry process gone and reaped", pid, err)
	}
	if _, err := p.opts.stdin.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Errorf("reading the relayed stdin = %v, want it closed by the exchange", err)
	}
	for _, fifo := range []string{filepath.Join(p.dir, "tty"), p.doneFifo} {
		if n := fdsPointingAt(t, fifo); n != 0 {
			t.Errorf("%d descriptors still open on %q", n, fifo)
		}
	}
}

// A popup that never announces anything cannot be waited on forever, and the
// proxy is its own reader on the tty FIFO, so no EOF is coming: the read
// deadline is the only thing that ends it, and pinentry is never started.
func TestCallPinentry_ttyThatIsNeverAnnounced(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = staysSilent
	p.opts.Timeouts.TTYRead = 150 * time.Millisecond
	p.feed(t, "BYE\n")

	start := time.Now()
	err := CallPinentry(t.Context(), p.backend, p.opts)
	elapsed := time.Since(start)

	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want the tty read deadline to end the wait", err)
	}
	if !strings.Contains(err.Error(), "scan failed") {
		t.Errorf("err = %v, want the stage that failed named", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("CallPinentry waited %v, far past the %v bound",
			elapsed, p.opts.Timeouts.TTYRead)
	}
	if _, err := os.Stat(p.pidfile); err == nil {
		t.Error("pinentry was started although no tty was ever announced")
	}
}
