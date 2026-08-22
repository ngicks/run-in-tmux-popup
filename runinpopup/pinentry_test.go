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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

// popupTTY is the tty the fake popup announces. It never has to exist: the
// proxy only ever forwards the name.
const popupTTY = "/dev/pts/42"

// fakePinentryCommand is the argv[0] after the program name that turns this
// test binary into a stand-in for the pinentry executable. It travels in
// PinentryArgs — which the launcher passes through untouched — rather than in
// the environment, so the dispatch cannot reach the popup payload or any other
// subprocess a test starts.
const fakePinentryCommand = "fake-pinentry"

const (
	// pinentryReadsUntilBye records the forwarded stream verbatim and exits on
	// the line that ends a real Assuan exchange.
	pinentryReadsUntilBye = "read-until-bye"
	// pinentryReadsUntilEOF records the forwarded stream verbatim and exits only
	// once the proxy closes its Assuan input.
	pinentryReadsUntilEOF = "read-until-eof"
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
		if err != nil || mode == pinentryReadsUntilBye && bytes.Equal(line, []byte("BYE\n")) {
			break
		}
	}
	if err := os.WriteFile(transcript, forwarded.Bytes(), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	return 0, true
}

// ttyHandshakeBackend is shellBackend hosting the tty handshake, so the whole
// exchange can be driven without a terminal multiplexer: its popup payload runs
// in a local shell and speaks the FIFO protocol the real backends' payloads
// speak.
type ttyHandshakeBackend struct {
	shellBackend
	// script builds the popup payload from the two FIFO paths. nil announces
	// popupTTY and then waits to be dismissed, which is what every real backend
	// does.
	script       func(ttyFifo, doneFifo string) string
	validateTTY  func(line string) (string, error)
	handshakeErr error
}

func (b *ttyHandshakeBackend) NewTTYHandshake(
	ttyFifo, doneFifo string,
) (TTYHandshake, error) {
	if b.handshakeErr != nil {
		return TTYHandshake{}, b.handshakeErr
	}
	script := b.script
	if script == nil {
		script = announceThenWait
	}
	return TTYHandshake{
		Spec:        PopupSpec{Script: script(ttyFifo, doneFifo)},
		ValidateTTY: b.validateTTY,
	}, nil
}

func announceThenWait(ttyFifo, doneFifo string) string {
	return fmt.Sprintf("echo %s >> %s && read done < %s",
		shellargv.Quote(popupTTY), shellargv.Quote(ttyFifo), shellargv.Quote(doneFifo))
}

// announceThenExit is a popup that goes away the moment it has reported its
// tty, the way a dismissed one does.
func announceThenExit(ttyFifo, _ string) string {
	return fmt.Sprintf("echo %s >> %s", shellargv.Quote(popupTTY), shellargv.Quote(ttyFifo))
}

// staysSilent is a popup that is alive but never reports anything, so only the
// tty read deadline can end the wait.
func staysSilent(_, _ string) string { return "sleep 5" }

// pinentryProxy is one wired-up exchange: a popup that touches no multiplexer,
// this test binary standing in for the pinentry executable, a pipe standing in
// for the Assuan input the proxy relays, and files standing in for the two
// outputs, which are handed to the pinentry child as they are.
type pinentryProxy struct {
	dir        string
	launcher   *PinentryLauncher
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
		backend:    new(ttyHandshakeBackend),
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

	p.launcher = &PinentryLauncher{
		Popup: &PopupLauncher{
			Backend: p.backend,
			// The test owns the directory, so the handshake FIFOs land where the
			// assertions below look for them.
			Workspace: WorkspaceOptions{Dir: dir},
		},
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

func TestPinentryLauncher_Call_requiresHandshaker(t *testing.T) {
	launcher := &PinentryLauncher{
		Popup: &PopupLauncher{
			Backend:   &shellBackend{},
			Workspace: WorkspaceOptions{Dir: t.TempDir()},
		},
	}
	err := launcher.Call(t.Context())
	if err == nil || !strings.Contains(err.Error(), "TTYHandshaker") {
		t.Fatalf("err = %v, want it to name the missing interface", err)
	}
	if err := (&PinentryLauncher{}).Call(t.Context()); err == nil {
		t.Fatal("a launcher without a popup launcher must be rejected")
	}
}

// The one line gpg-agent sends that the proxy exists to rewrite: pinentry must
// draw in the popup, not on the terminal gpg-agent happened to pick. Everything
// around it — including the \r\n bufio.Scanner strips — is pinned byte for byte,
// since this is the exact stream the pinentry child sees.
//
// The exchange ends by closing its end of the relayed stdin, so this also pins
// that the close it causes itself is not reported as a failure.
func TestPinentryLauncher_Call_rewritesTheAnnouncedTTYIntoTheStream(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.feed(t, "OPTION lc-ctype=en_US.UTF-8\n"+
		"OPTION ttyname=/dev/pts/9\n"+
		"OPTION ttytype=xterm-256color\n"+
		"OPTION grab\r\n"+
		"SETDESC Enter+passphrase\n"+
		"GETPIN\n"+
		"BYE\n")

	if err := p.launcher.Call(t.Context()); err != nil {
		t.Fatalf("Call: %v", err)
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

// Pinentry implementations may wait for EOF after the upstream Assuan stream
// ends. The process wait is already running then, so relay completion must close
// the child's stdin to let that wait finish and the popup be dismissed.
func TestPinentryLauncher_Call_assuanEOFClosesPinentryInputAndDismissesPopup(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilEOF)
	p.backend.script = announceThenExit
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "GETPIN\n")
	if err := p.stdin.Close(); err != nil {
		t.Fatalf("closing the assuan stream: %v", err)
	}

	if err := p.launcher.Call(t.Context()); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := p.forwarded(t); got != "GETPIN\n" {
		t.Errorf("forwarded %q, want %q", got, "GETPIN\n")
	}
	if got := dismissal(); got != "done\n" {
		t.Errorf("done fifo carried %q, want %q", got, "done\n")
	}
}

// Only a line that starts with the option, spelled exactly that way, is
// rewritten; every near miss and everything else reaches pinentry as it came.
func TestPinentryLauncher_Call_forwardsEverythingElseByteForByte(t *testing.T) {
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

	if err := p.launcher.Call(t.Context()); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := p.forwarded(t); got != stream {
		t.Errorf("forwarded to pinentry:\n%q\nwant it unchanged:\n%q", got, stream)
	}
}

// A popup that is dismissed the moment it has reported its tty is invisible to
// the proxy: both FIFOs are opened read-write, so neither the tty read nor the
// dismissal lacks a peer, and the exchange finishes exactly as if the popup were
// still there — dismissal bytes included.
func TestPinentryLauncher_Call_popupThatGoesAwayIsNotNoticed(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = announceThenExit
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "GETPIN\nBYE\n")

	if err := p.launcher.Call(t.Context()); err != nil {
		t.Fatalf("Call: %v", err)
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
func TestPinentryLauncher_Call_pinentryThatCannotStart(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = announceThenExit
	p.launcher.PinentryPath = filepath.Join(p.dir, "no-such-pinentry")
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "BYE\n")

	err := p.launcher.Call(t.Context())

	if err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("err = %v, want the pinentry start failure surfaced", err)
	}
	if !strings.Contains(err.Error(), p.launcher.PinentryPath) {
		t.Errorf("err = %v, want it to name %q", err, p.launcher.PinentryPath)
	}
	if got := dismissal(); got != "done\n" {
		t.Errorf("done fifo carried %q, want the popup dismissed anyway", got)
	}
}

// Cancelling mid-prompt is the ordinary way a passphrase request ends: the user
// walks away and gpg-agent's deadline fires. Everything the exchange holds has
// to go with it — the pinentry process and the two handshake FIFOs — while the
// stdio it was handed has to survive: gpg-agent speaks Assuan over this
// process's own streams, which are none of this package's business to close.
func TestPinentryLauncher_Call_cancellationReleasesTheProcessAndPipes(t *testing.T) {
	p := newPinentryProxy(t, pinentryHangs)
	p.feed(t, "GETPIN\n")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- p.launcher.Call(ctx) }()

	pid := p.pinentryPid(t)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("a canceled exchange must report an error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Call never returned after the cancellation")
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("kill(%d, 0) = %v, want the pinentry process gone and reaped", pid, err)
	}
	// A closed read end would make this write fail with EPIPE.
	if _, err := p.stdin.WriteString("BYE\n"); err != nil {
		t.Errorf("writing the relayed stdin = %v, want the exchange to have left it open", err)
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
func TestPinentryLauncher_Call_ttyThatIsNeverAnnounced(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = staysSilent
	p.launcher.Timeouts.TTYRead = 150 * time.Millisecond
	p.feed(t, "BYE\n")

	start := time.Now()
	err := p.launcher.Call(t.Context())
	elapsed := time.Since(start)

	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want the tty read deadline to end the wait", err)
	}
	if !strings.Contains(err.Error(), "scan failed") {
		t.Errorf("err = %v, want the stage that failed named", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Call waited %v, far past the %v bound",
			elapsed, p.launcher.Timeouts.TTYRead)
	}
	if _, err := os.Stat(p.pidfile); err == nil {
		t.Error("pinentry was started although no tty was ever announced")
	}
}

// A backend that cannot build its handshake fails the exchange before any
// popup is opened: there is no spec to launch, so nothing to dismiss and no
// pinentry to start.
func TestPinentryLauncher_Call_handshakeThatCannotBeBuilt(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.handshakeErr = errors.New("this mechanism has no fifo channel")
	p.feed(t, "BYE\n")

	err := p.launcher.Call(t.Context())

	if !errors.Is(err, p.backend.handshakeErr) {
		t.Fatalf("err = %v, want it to wrap the handshake failure", err)
	}
	if !strings.Contains(err.Error(), "building tty handshake") {
		t.Errorf("err = %v, want the stage that failed named", err)
	}
	if len(p.backend.launched) != 0 {
		t.Error("a popup was launched although the handshake could not be built")
	}
	if _, err := os.Stat(p.pidfile); err == nil {
		t.Error("pinentry was started although the handshake could not be built")
	}
}

// A backend's ValidateTTY is its guard on what its popup could have written;
// an announcement it rejects fails the exchange, while the popup is still
// dismissed. The popup exits after announcing so the dismissal bytes have one
// reader — the watcher — to land on.
func TestPinentryLauncher_Call_validationRejectsTheAnnouncement(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	rejected := errors.New("not a terminal this popup could be on")
	p.backend.script = announceThenExit
	p.backend.validateTTY = func(string) (string, error) { return "", rejected }
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "BYE\n")

	err := p.launcher.Call(t.Context())

	if !errors.Is(err, rejected) {
		t.Fatalf("err = %v, want the validation failure", err)
	}
	if got := dismissal(); got != "done\n" {
		t.Errorf("done fifo carried %q, want the popup dismissed anyway", got)
	}
	if _, err := os.Stat(p.pidfile); err == nil {
		t.Error("pinentry was started on a rejected announcement")
	}
}

// An empty announcement is a popup that reached the FIFO but had nothing to
// say — there is no terminal to point pinentry at, and no answer is an error,
// not a prompt on an empty tty name.
func TestPinentryLauncher_Call_emptyAnnouncement(t *testing.T) {
	p := newPinentryProxy(t, pinentryReadsUntilBye)
	p.backend.script = func(ttyFifo, _ string) string {
		return fmt.Sprintf("echo '' >> %s", shellargv.Quote(ttyFifo))
	}
	dismissal := watchDone(t, p.doneFifo)
	p.feed(t, "BYE\n")

	err := p.launcher.Call(t.Context())

	if err == nil || !strings.Contains(err.Error(), "empty tty") {
		t.Fatalf("err = %v, want the empty announcement refused", err)
	}
	if got := dismissal(); got != "done\n" {
		t.Errorf("done fifo carried %q, want the popup dismissed anyway", got)
	}
	if _, err := os.Stat(p.pidfile); err == nil {
		t.Error("pinentry was started although no terminal was announced")
	}
}

// fakeRendezvous is a popup the exchange never has to open: it announces a
// terminal on demand and records having been dismissed.
type fakeRendezvous struct {
	tty        string
	acquireErr error
	dismissErr error
	dismissed  int
}

func (f *fakeRendezvous) acquire(context.Context) (string, error) {
	return f.tty, f.acquireErr
}

func (f *fakeRendezvous) dismiss() error {
	f.dismissed++
	return f.dismissErr
}

// fakePinentry is a pinentry that never runs: it records the stream the
// exchange rewrites into it and ends the wait on the line that ends a real
// Assuan exchange or when its input is closed, the ways the binary exits.
type fakePinentry struct {
	startErr error
	waitErr  error
	started  int

	mu       sync.Mutex
	received bytes.Buffer
	byeSeen  chan struct{}
	// seeBye ends the wait, whether the exchange forwarded the line or a test
	// stands in for one that never had to.
	seeBye func()
}

func newFakePinentry() *fakePinentry {
	f := &fakePinentry{byeSeen: make(chan struct{})}
	f.seeBye = sync.OnceFunc(func() { close(f.byeSeen) })
	return f
}

func (f *fakePinentry) start(context.Context) (io.WriteCloser, error) {
	f.started++
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f, nil
}

func (f *fakePinentry) wait() error {
	if f.startErr == nil {
		<-f.byeSeen
	}
	return f.waitErr
}

func (f *fakePinentry) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.received.Write(p)
	if bytes.Contains(f.received.Bytes(), []byte("BYE\n")) {
		f.seeBye()
	}
	return n, err
}

func (f *fakePinentry) Close() error {
	f.seeBye()
	return nil
}

func (f *fakePinentry) forwarded() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.received.String()
}

func TestPinentryExchange_run(t *testing.T) {
	newExchange := func(
		rendezvous *fakeRendezvous,
		pinentry *fakePinentry,
		input io.ReadCloser,
	) *pinentryExchange {
		return &pinentryExchange{
			rendezvous: rendezvous,
			pinentry:   pinentry,
			input:      input,
			logger:     loggerOrDiscard(nil),
		}
	}

	t.Run("the announced terminal reaches pinentry", func(t *testing.T) {
		rendezvous := &fakeRendezvous{tty: popupTTY}
		pinentry := newFakePinentry()
		// Left open after the stream, as gpg-agent leaves its end: what ends the
		// relay is the exchange closing this end, not the sender stopping.
		pr, pw := io.Pipe()
		go func() {
			_, _ = pw.Write([]byte("OPTION ttyname=/dev/pts/9\nBYE\n"))
		}()

		if err := newExchange(rendezvous, pinentry, pr).run(t.Context()); err != nil {
			t.Fatalf("run: %v", err)
		}
		want := "OPTION ttyname=" + popupTTY + "\nBYE\n"
		if got := pinentry.forwarded(); got != want {
			t.Errorf("forwarded %q, want %q", got, want)
		}
		if rendezvous.dismissed != 1 {
			t.Errorf("dismissed %d times, want exactly one", rendezvous.dismissed)
		}
	})

	t.Run("a relay failure is reported", func(t *testing.T) {
		relayErr := errors.New("the assuan input failed")
		rendezvous := &fakeRendezvous{tty: popupTTY}
		pinentry := newFakePinentry()

		errCh := make(chan error, 1)
		go func() {
			errCh <- newExchange(
				rendezvous,
				pinentry,
				io.NopCloser(errReader{relayErr}),
			).run(t.Context())
		}()

		var err error
		select {
		case err = <-errCh:
		case <-time.After(5 * time.Second):
			t.Fatal("run never returned after the relay failed")
		}

		if !errors.Is(err, relayErr) {
			t.Fatalf("err = %v, want the relay failure %v", err, relayErr)
		}
		if rendezvous.dismissed != 1 {
			t.Errorf("dismissed %d times, want exactly one", rendezvous.dismissed)
		}
	})

	t.Run("a popup that announced nothing is still dismissed", func(t *testing.T) {
		acquireErr := errors.New("scan failed")
		rendezvous := &fakeRendezvous{acquireErr: acquireErr}
		pinentry := newFakePinentry()

		err := newExchange(rendezvous, pinentry, io.NopCloser(strings.NewReader(""))).
			run(t.Context())

		if !errors.Is(err, acquireErr) {
			t.Fatalf("err = %v, want it to wrap %v", err, acquireErr)
		}
		if rendezvous.dismissed != 1 {
			t.Errorf("dismissed %d times, want the popup told to go away anyway",
				rendezvous.dismissed)
		}
		if pinentry.started != 0 {
			t.Error("pinentry was started although no terminal was ever announced")
		}
	})

	t.Run("pinentry's failure outranks a failed dismissal", func(t *testing.T) {
		waitErr := errors.New("pinentry failed")
		rendezvous := &fakeRendezvous{tty: popupTTY, dismissErr: errors.New("writing done fifo")}
		pinentry := newFakePinentry()
		pinentry.waitErr = waitErr
		pinentry.seeBye()

		err := newExchange(rendezvous, pinentry, io.NopCloser(strings.NewReader(""))).
			run(t.Context())

		if !errors.Is(err, waitErr) {
			t.Fatalf("err = %v, want the pinentry failure, not the dismissal", err)
		}
	})

	t.Run("a failed dismissal is reported when nothing else failed", func(t *testing.T) {
		dismissErr := errors.New("writing done fifo")
		rendezvous := &fakeRendezvous{tty: popupTTY, dismissErr: dismissErr}
		pinentry := newFakePinentry()
		pinentry.seeBye()

		err := newExchange(rendezvous, pinentry, io.NopCloser(strings.NewReader(""))).
			run(t.Context())

		if !errors.Is(err, dismissErr) {
			t.Fatalf("err = %v, want the dismissal failure", err)
		}
	})
}
