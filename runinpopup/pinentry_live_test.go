// The tests in this file drive the whole pinentry proxy against real programs —
// a private tmux server with a real attached client, the run-in-popup binary and
// a real pinentry-curses — so they stay out of the default suite. Enable them
// with
//
//	go test -tags integration ./runinpopup/
//
// on a host that permits private tmux sockets and pty allocation: every server
// lives on its own -L socket, never the user's default one, and the client it is
// driven through is a pty the test made for it.
//
//go:build integration

package runinpopup

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const (
	// liveSession is the tmux session the popup opens in.
	liveSession = "probe"
	// livePassphrase is typed at the prompt and read back off the Assuan stream,
	// which is what makes this a completed prompt rather than a popup that merely
	// appeared.
	livePassphrase = "correct-horse"
	// liveCloseDeadline is how long the popup and the proxy have to go away once
	// the exchange is over. It is deliberately far below liveOverall: a popup that
	// only closes when the whole exchange times out is the bug, not a pass.
	liveCloseDeadline = 5 * time.Second
	// liveOverall bounds the proxy's own exchange. A hang has to end somewhere, and
	// the two-minute default would be the whole test's runtime.
	liveOverall = 30 * time.Second
	// liveReplyTimeout bounds one Assuan reply. The first one waits for the popup
	// to open and pinentry to start, so it is the generous one.
	liveReplyTimeout = 30 * time.Second
)

// liveSeq keeps two live runs in one test binary apart: they must not land on
// one tmux server.
var liveSeq atomic.Uint64

// syncBuffer collects what a process wrote while the test reads it from another
// goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func ioctl(fd, request, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, arg); errno != 0 {
		return errno
	}
	return nil
}

// openPTY allocates a pty pair sized like an ordinary terminal.
//
// A tmux popup is a client-side overlay, so the whole exchange needs a client
// with a terminal to draw it on — "no current client" is all a server without one
// answers. This test therefore has to be that terminal, and the master end is
// both what it reads the popup off and what it types the passphrase into.
func openPTY(t *testing.T) (master, terminal *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot allocate a pty: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	var unlock int32
	if err := ioctl(m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		t.Fatalf("unlocking the pty: %v", err)
	}
	var index uint32
	if err := ioctl(m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&index))); err != nil {
		t.Fatalf("naming the pty: %v", err)
	}
	// Roomy enough that a popup — half the terminal in each direction by default —
	// still has room for the description the prompt is recognized by.
	size := struct{ rows, cols, x, y uint16 }{40, 120, 0, 0}
	if err := ioctl(m.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size))); err != nil {
		t.Fatalf("sizing the pty: %v", err)
	}

	term, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", index), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening the pty terminal: %v", err)
	}
	return m, term
}

// liveTmux is a private tmux server with one session and one real client attached
// to a pty this test owns.
type liveTmux struct {
	path       string
	socketPath string
	// client is the attached client's name, which is what display-popup is given
	// so the popup lands on it rather than on whichever client tmux would
	// otherwise call the current one.
	client string
	// screen is everything the client's terminal was sent, which is where the
	// popup's contents show up.
	screen *syncBuffer
	// keys is the client's terminal, written to as a user types.
	keys *os.File
}

// startLiveTmux brings up the server, the session and the attached client.
func startLiveTmux(t *testing.T) *liveTmux {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}

	// Pid plus a counter, kept short rather than descriptive: two concurrent "go
	// test" runs must not land on one server, neither may ever land on the default
	// socket, and the socket path has to fit in a sockaddr_un.
	socket := fmt.Sprintf("rip-live-%d-%d", os.Getpid(), liveSeq.Add(1))
	t.Logf("scratch tmux socket: %s", socket)

	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(tmuxPath, append([]string{"-L", socket}, args...)...).
			CombinedOutput()
		if err != nil {
			t.Fatalf("tmux -L %s %s: %v: %s", socket, strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// socketPath is filled in below; the cleanup reads it when it runs, so a
	// failure between here and then still kills the server.
	var socketPath string
	t.Cleanup(func() {
		_ = exec.Command(tmuxPath, "-L", socket, "kill-server").Run()
		if socketPath != "" {
			_ = os.Remove(socketPath)
		}
	})

	run("new-session", "-d", "-s", liveSession, "sleep 600")
	socketPath = run("display-message", "-p", "-t", liveSession, "#{socket_path}")

	master, terminal := openPTY(t)
	screen := new(syncBuffer)
	// Drained for as long as the client lives: a terminal nobody reads fills up,
	// and tmux then stops drawing the popup this test is watching for.
	go func() { _, _ = io.Copy(screen, master) }()

	attach := exec.Command(tmuxPath, "-L", socket, "attach", "-t", liveSession)
	attach.Stdin, attach.Stdout, attach.Stderr = terminal, terminal, terminal
	attach.Env = append(os.Environ(), "TERM=xterm-256color")
	// A tmux client insists on a controlling terminal, which is the pty above.
	attach.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := attach.Start(); err != nil {
		// Nobody took the terminal end over, and only the master has a cleanup of
		// its own.
		terminal.Close()
		t.Fatalf("attaching a tmux client: %v", err)
	}
	// The client owns the terminal end now; this process keeps only the master.
	terminal.Close()
	t.Cleanup(func() {
		_ = attach.Process.Kill()
		_, _ = attach.Process.Wait()
	})

	live := &liveTmux{path: tmuxPath, socketPath: socketPath, screen: screen, keys: master}
	deadline := time.Now().Add(20 * time.Second)
	for live.client == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no tmux client ever attached; the client terminal saw %q", screen.String())
		}
		time.Sleep(20 * time.Millisecond)
		live.client = run("list-clients", "-F", "#{client_name}")
	}
	t.Logf("attached tmux client: %s", live.client)
	return live
}

// typeAt types at the client's terminal, ending the line the way the return key
// does. A popup takes the client's keys while it is open, so this is how the
// passphrase reaches the pinentry drawing in it.
func (l *liveTmux) typeAt(t *testing.T, s string) {
	t.Helper()
	if _, err := l.keys.WriteString(s + "\r"); err != nil {
		t.Fatalf("typing at the client terminal: %v", err)
	}
}

// awaitScreen waits for text to appear on the client's terminal.
func (l *liveTmux) awaitScreen(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(l.screen.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never appeared on the client terminal within %v", want, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// popupPayloadFIFOEnv names the variable a tmux popup opened for a tty handshake
// receives the path of the FIFO it announces its terminal on. The payload script
// takes the FIFOs through popup env rather than in its command line, so this is
// where the path itself can be read back.
const popupPayloadFIFOEnv = "TTY_FIFO_FILE"

// popupPayloads reports the command lines of this run's handshake payloads
// running right now — the shell inside the popup, which lives exactly as long as
// the popup does, since display-popup -E closes the popup when its payload exits.
//
// Only this run's popups count. The proxy was given a temp directory of its own,
// so the FIFO paths it hands a popup are unique to the run, and a payload is
// recognized by one of them being in its environment: a concurrent integration
// run, or a popup somebody left open on this host, names a different path and is
// passed over.
//
// The tmux client that opened the popup names those same FIFOs and is
// deliberately not counted: it passes them as popup env in its own argv rather
// than carrying them in its environment, and it is interrupted while the popup it
// opened lives on, so only the shell answers whether the popup is still there. A
// shell is what "-c" as the first argument identifies; the client has flags
// there, and so do the short-lived programs the payload itself runs with the same
// environment.
func (p *livePinentry) popupPayloads(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skipf("cannot inspect running processes: %v", err)
	}
	// The trailing separator matters: one run's temp directory can be a string
	// prefix of another's.
	ours := popupPayloadFIFOEnv + "=" + p.fifoRoot + string(filepath.Separator)
	var found []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		argv := strings.Split(string(bytes.TrimRight(raw, "\x00")), "\x00")
		if len(argv) < 2 || argv[1] != "-c" {
			continue
		}
		// Another user's process is unreadable here, which is answer enough: it
		// cannot be a popup this run opened.
		environ, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		vars := strings.Split(string(bytes.TrimRight(environ, "\x00")), "\x00")
		if !slices.ContainsFunc(vars, func(v string) bool { return strings.HasPrefix(v, ours) }) {
			continue
		}
		found = append(found, e.Name()+": "+strings.Join(argv, " "))
	}
	return found
}

// assuan is the gpg-agent side of the exchange: the proxy's own stdin and stdout,
// spoken to line by line the way gpg-agent speaks to a pinentry.
type assuan struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Reader
	// raw is out's file, whose deadline is what keeps a proxy that answers nothing
	// from stalling the test instead of failing it.
	raw *os.File
}

func (a *assuan) send(line string) {
	a.t.Helper()
	if _, err := io.WriteString(a.in, line+"\n"); err != nil {
		a.t.Fatalf("sending %q: %v", line, err)
	}
}

func (a *assuan) readLine(timeout time.Duration) string {
	a.t.Helper()
	if err := a.raw.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		a.t.Fatalf("bounding the assuan read: %v", err)
	}
	line, err := a.out.ReadString('\n')
	if err != nil {
		a.t.Fatalf("reading the assuan reply: %v (read %q so far)", err, line)
	}
	return strings.TrimRight(line, "\r\n")
}

// expectOK sends a command and insists on the acknowledgement a pinentry gives it.
func (a *assuan) expectOK(line string) {
	a.t.Helper()
	a.send(line)
	if reply := a.readLine(liveReplyTimeout); !strings.HasPrefix(reply, "OK") {
		a.t.Fatalf("%s answered %q, want an OK", line, reply)
	}
}

// buildRunInPopup builds the binary gpg-agent would run. The proxy is exercised
// as the program it ships as — its own process, its own stdio — because what the
// bug turned on is which process holds those descriptors.
func buildRunInPopup(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "run-in-popup")
	build := exec.Command("go", "build", "-o", bin, "../cmd/run-in-popup")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building run-in-popup: %v: %s", err, out)
	}
	return bin
}

// livePinentry is one running proxy: the process gpg-agent would have started,
// the Assuan connection to it and the popup it opened.
type livePinentry struct {
	tmux   *liveTmux
	assuan *assuan
	proxy  *exec.Cmd
	// in and out are the connection's two descriptors, held apart from the assuan
	// wrapper so a test can drop them the way gpg-agent drops its own.
	in  io.WriteCloser
	out *os.File
	// marker is the description pinentry draws in the popup, distinct per run so
	// the prompt is recognized rather than guessed at.
	marker string
	// fifoRoot is the temp directory the proxy creates its workspace under, which
	// is what makes this run's popup distinguishable from anyone else's.
	fifoRoot string
	stderr   *syncBuffer
	// exited is closed once the proxy has been reaped, after which waitErr says how
	// it went.
	exited  chan struct{}
	waitErr error
}

// startLivePinentry opens the popup and gets as far as pinentry's greeting, which
// only arrives once the popup has announced the terminal pinentry was started on.
func startLivePinentry(t *testing.T) *livePinentry {
	t.Helper()
	pinentryPath, err := exec.LookPath("pinentry-curses")
	if err != nil {
		t.Skipf("pinentry-curses is not installed: %v", err)
	}
	bin := buildRunInPopup(t)
	live := startLiveTmux(t)

	p := &livePinentry{
		tmux:     live,
		marker:   fmt.Sprintf("runinpopup-live-%d-%d", os.Getpid(), liveSeq.Load()),
		fifoRoot: t.TempDir(),
		stderr:   new(syncBuffer),
		exited:   make(chan struct{}),
	}

	p.proxy = exec.Command(bin, "pinentry", "--backend", "tmux-popup")
	// Built rather than inherited: the proxy detects its multiplexer from the
	// environment, and a test running inside the developer's own tmux would
	// otherwise open the popup there.
	p.proxy.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TERM=xterm-256color",
		"PINENTRY_USER_DATA=" + strings.Join([]string{
			"TMUX_POPUP", live.path, liveSession, live.client, live.socketPath + ",0,0",
		}, ":"),
		// A path that does not exist, so the developer's own config file has no say
		// in what this test measures.
		ENV_RUN_IN_POPUP_CONF + "=" + filepath.Join(t.TempDir(), "no-config.json"),
		// The proxy creates its workspace under this directory, which is what gives
		// the handshake FIFOs paths no other run on this host can be holding — the
		// popup is identified by them.
		"TMPDIR=" + p.fifoRoot,
		EnvPrefix + "PINENTRY_PATH=" + pinentryPath,
		EnvPrefix + "TIMEOUTS_OVERALL=" + liveOverall.String(),
	}
	p.proxy.Stderr = p.stderr
	// The proxy's stderr is a buffer rather than a file, so os/exec relays it
	// through a pipe its Wait joins. A bound keeps a proxy whose pipes outlive it
	// from turning the teardown into a second hang.
	p.proxy.WaitDelay = 2 * time.Second

	in, err := p.proxy.StdinPipe()
	if err != nil {
		t.Fatalf("opening the proxy's assuan input: %v", err)
	}
	out, err := p.proxy.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the proxy's assuan output: %v", err)
	}
	raw, ok := out.(*os.File)
	if !ok {
		t.Fatalf("the proxy's assuan output is %T, want a file to bound reads on", out)
	}
	p.in, p.out = in, raw

	if err := p.proxy.Start(); err != nil {
		t.Fatalf("starting the proxy: %v", err)
	}
	// Closed rather than sent on: the exit is awaited both by an assertion and by
	// the cleanup, and a value could only be taken once.
	go func() { defer close(p.exited); p.waitErr = p.proxy.Wait() }()
	t.Cleanup(func() {
		_ = p.in.Close()
		_ = p.proxy.Process.Kill()
		select {
		case <-p.exited:
		case <-time.After(liveCloseDeadline):
			t.Error("the proxy could not be reaped even after being killed")
		}
		if s := p.stderr.String(); s != "" {
			t.Logf("the proxy's stderr:\n%s", s)
		}
	})

	p.assuan = &assuan{t: t, in: in, out: bufio.NewReader(out), raw: raw}
	if greeting := p.assuan.readLine(liveReplyTimeout); !strings.HasPrefix(greeting, "OK") {
		t.Fatalf("the proxy greeted with %q, want a pinentry OK", greeting)
	}
	if len(p.popupPayloads(t)) == 0 {
		t.Fatal("no popup payload is running although pinentry greeted")
	}
	return p
}

// enterPassphrase runs the prompt the way a user answers one: the options
// gpg-agent sets, GETPIN, and the passphrase typed at the popup once it is really
// on screen.
func (p *livePinentry) enterPassphrase(t *testing.T) {
	t.Helper()
	// The line the proxy exists to rewrite: gpg-agent names the terminal it
	// inherited, and the prompt has to land in the popup instead.
	p.assuan.expectOK("OPTION ttyname=/dev/pts/9999")
	p.assuan.expectOK("OPTION ttytype=xterm-256color")
	p.assuan.expectOK("OPTION lc-ctype=C.UTF-8")
	p.assuan.expectOK("SETDESC " + p.marker)
	p.assuan.expectOK("SETPROMPT PIN:")

	p.assuan.send("GETPIN")
	p.tmux.awaitScreen(t, p.marker, liveReplyTimeout)
	p.tmux.typeAt(t, livePassphrase)

	if got, want := p.assuan.readLine(liveReplyTimeout), "D "+livePassphrase; got != want {
		t.Fatalf("GETPIN answered %q, want %q", got, want)
	}
	if got := p.assuan.readLine(liveReplyTimeout); !strings.HasPrefix(got, "OK") {
		t.Fatalf("GETPIN ended with %q, want an OK", got)
	}
}

// requirePopupClosed insists the popup goes away on its own deadline. It is what
// the user sees, so it is asserted first and separately: a popup that outlives the
// prompt is the whole bug, whatever the proxy process then does.
func (p *livePinentry) requirePopupClosed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(liveCloseDeadline)
	for {
		payloads := p.popupPayloads(t)
		if len(payloads) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"the popup was still open %v after the exchange ended; still running:\n%s\n%s",
				liveCloseDeadline, strings.Join(payloads, "\n"), p.diagnose(t),
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// requireExited insists the proxy is gone too, and that it left of its own accord:
// a proxy killed by a signal is one that never reached the dismissal it owes the
// popup, which is how the popup came to outlive the prompt in the first place.
func (p *livePinentry) requireExited(t *testing.T) {
	t.Helper()
	select {
	case <-p.exited:
	case <-time.After(liveCloseDeadline):
		t.Fatalf("the proxy was still running %v after the exchange ended\n%s",
			liveCloseDeadline, p.diagnose(t))
	}
	status, ok := p.proxy.ProcessState.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		t.Fatalf("the proxy was killed by %v, want it to have exited on its own", status.Signal())
	}
}

// diagnose accounts for the proxy at the moment something it owed did not happen.
//
// A proxy that is still there is asked what it is stuck on: SIGQUIT is uncaught,
// so the Go runtime prints every goroutine's stack to the process's stderr on the
// way out. One that is already gone cannot be asked, and how it went is the
// account instead — the death that leaves a popup behind is one the proxy never
// got to report.
func (p *livePinentry) diagnose(t *testing.T) string {
	t.Helper()
	select {
	case <-p.exited:
		return fmt.Sprintf("the proxy had already exited with %v", p.waitErr)
	default:
	}
	if err := p.proxy.Process.Signal(syscall.SIGQUIT); err != nil {
		return fmt.Sprintf("the proxy could not be asked for a goroutine dump: %v", err)
	}
	select {
	case <-p.exited:
	// Long enough for the runtime to finish printing, short enough not to become a
	// second hang.
	case <-time.After(2 * time.Second):
	}
	return "the proxy was dumped on SIGQUIT; its stacks are in the stderr logged below"
}

// A pinentry prompt that the user answers has to leave nothing behind: the popup
// closes and the proxy exits as soon as gpg-agent is done with it, not whenever
// the overall timeout gets around to it.
//
// gpg-agent does not have to close its end for that to happen — it holds the
// connection for as long as it likes — so neither does this.
func TestPinentryLauncher_liveTmuxPopupClosesWhenTheExchangeEnds(t *testing.T) {
	p := startLivePinentry(t)
	p.enterPassphrase(t)

	p.assuan.send("BYE")
	if got := p.assuan.readLine(liveReplyTimeout); !strings.HasPrefix(got, "OK") {
		t.Fatalf("BYE answered %q, want an OK", got)
	}
	// Kept open on purpose until the assertions are done, the way gpg-agent may
	// keep it: what has to end the exchange is the exchange being over, not the
	// proxy being handed an EOF it can act on.
	defer p.in.Close()

	p.requirePopupClosed(t)
	p.requireExited(t)
	if p.waitErr != nil {
		t.Errorf("the proxy exited with %v, want a clean end", p.waitErr)
	}
}

// The way the connection really ends: libassuan's pipe client says BYE and drops
// both descriptors at once, without waiting to be acknowledged. Whatever pinentry
// writes after that lands on a pipe with no reader — and a broken pipe on fd 1 is
// fatal, so it matters a great deal whose fd 1 it is. It has to be the pinentry
// child's, whose death the proxy is still around to report and to dismiss the
// popup after; a proxy that writes those bytes itself is killed holding a popup
// nobody else will ever close.
func TestPinentryLauncher_liveTmuxPopupClosesWhenGpgAgentHangsUp(t *testing.T) {
	p := startLivePinentry(t)
	p.enterPassphrase(t)

	p.assuan.send("BYE")
	if err := p.out.Close(); err != nil {
		t.Fatalf("dropping the assuan output: %v", err)
	}
	if err := p.in.Close(); err != nil {
		t.Fatalf("dropping the assuan input: %v", err)
	}

	p.requirePopupClosed(t)
	p.requireExited(t)
}
