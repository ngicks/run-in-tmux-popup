package backend

import (
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

func tmuxBackend(t *testing.T) *TmuxPopup {
	t.Helper()
	b, err := NewTmuxPopup(Options{
		BinaryPath:  "/usr/bin/tmux",
		ClientId:    "%1",
		SessionMeta: "/run/user/1000/tmux-1000/default,111,0",
	})
	if err != nil {
		t.Fatalf("NewTmuxPopup: %v", err)
	}
	return b
}

func tmuxFloatingPaneBackend(t *testing.T) *TmuxFloatingPane {
	t.Helper()
	b, err := NewTmuxFloatingPane(Options{
		BinaryPath:  "/usr/bin/tmux",
		SessionId:   "work",
		ClientId:    "%1",
		SessionMeta: "/run/user/1000/tmux-1000/default,111,0",
	})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPane: %v", err)
	}
	return b
}

func zellijBackend(t *testing.T) *Zellij {
	t.Helper()
	b, err := NewZellij(Options{
		BinaryPath: "/usr/bin/zellij",
		SessionId:  "session-id",
		Shell:      "/bin/bash",
	})
	if err != nil {
		t.Fatalf("NewZellij: %v", err)
	}
	return b
}

func assertPopupCommand(
	t *testing.T,
	gotPath string,
	gotArgs []string,
	wantPath string,
	wantArgs []string,
) {
	t.Helper()
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Errorf("args =\n\t%#v\nwant\n\t%#v", gotArgs, wantArgs)
	}
}

// The handshake argv is asserted literally: it is the one command line proven to
// work against a live tmux, so any change to it must be a deliberate edit here.
func TestTmuxPopup_PopupCommand_pinentryHandshake(t *testing.T) {
	b := tmuxBackend(t)

	handshake, err := b.NewPinentryHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewPinentryHandshake: %v", err)
	}
	prefix := handshake.Spec.Env["SEC_PREFIX"]
	suffix := handshake.Spec.Env["SEC_SUFFIX"]

	path, args := b.PopupCommand(handshake.Spec)
	assertPopupCommand(t, path, args, "/usr/bin/tmux", []string{
		"popup",
		"-c", "%1",
		"-e", "DONE_FIFO_FILE=/tmp/popup/done",
		"-e", "SEC_PREFIX=" + prefix,
		"-e", "SEC_SUFFIX=" + suffix,
		"-e", "TTY_FIFO_FILE=/tmp/popup/tty",
		"-E", "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE}" +
			" && read done < ${DONE_FIFO_FILE}",
	})
}

func TestTmuxPopup_PopupCommand_argvIsQuoted(t *testing.T) {
	b := tmuxBackend(t)

	path, args := b.PopupCommand(runinpopup.PopupSpec{
		Title:   "editor",
		Env:     map[string]string{"B": "2", "A": "1"},
		Command: []string{"vim", "my file.txt"},
	})
	assertPopupCommand(t, path, args, "/usr/bin/tmux", []string{
		"popup",
		"-c", "%1",
		"-T", "editor",
		"-e", "A=1",
		"-e", "B=2",
		"-E", `'vim' 'my file.txt'`,
	})
}

func TestTmuxPopup_PopupCommand_noClient(t *testing.T) {
	b, err := NewTmuxPopup(Options{TMUX: "/tmp/tmux-1000/default,1,0"})
	if err != nil {
		t.Fatalf("NewTmuxPopup: %v", err)
	}
	path, args := b.PopupCommand(runinpopup.PopupSpec{Command: []string{"true"}})
	assertPopupCommand(t, path, args, "tmux", []string{"popup", "-E", `'true'`})
}

func TestNewTmuxPopup_sessionMeta(t *testing.T) {
	// Malformed meta only matters when it is the value that will be exported.
	if _, err := NewTmuxPopup(Options{SessionMeta: "not-a-meta"}); err == nil {
		t.Error("malformed session meta must be rejected when $TMUX is unset")
	}
	if _, err := NewTmuxPopup(Options{
		SessionMeta: "not-a-meta",
		TMUX:        "/tmp/tmux-1000/default,1,0",
	}); err != nil {
		t.Errorf("session meta is unused when $TMUX is set, got %v", err)
	}
}

func TestTmuxPopup_Environ(t *testing.T) {
	meta := "/run/user/1000/tmux-1000/default,111,0"

	b := tmuxBackend(t)
	if got := b.Environ(); !slices.Equal(got, []string{"TMUX=" + meta}) {
		t.Errorf("Environ = %#v, want TMUX set from the session meta", got)
	}

	inside, err := NewTmuxPopup(Options{SessionMeta: meta, TMUX: "/other,2,0"})
	if err != nil {
		t.Fatalf("NewTmuxPopup: %v", err)
	}
	if got := inside.Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil when $TMUX is already set", got)
	}
}

func TestTmuxPopup_ValidateTTY(t *testing.T) {
	b := tmuxBackend(t)

	handshake, err := b.NewPinentryHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewPinentryHandshake: %v", err)
	}
	prefix := handshake.Spec.Env["SEC_PREFIX"]
	suffix := handshake.Spec.Env["SEC_SUFFIX"]
	if prefix == "" || suffix == "" || prefix == suffix {
		t.Fatalf("prefix = %q, suffix = %q: want two distinct secrets", prefix, suffix)
	}

	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"guarded tty", prefix + "/dev/pts/3" + suffix, "/dev/pts/3"},
		{"empty tty still unwraps", prefix + suffix, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := handshake.ValidateTTY(tc.line)
			if err != nil {
				t.Fatalf("ValidateTTY(%q): %v", tc.line, err)
			}
			if got != tc.want {
				t.Errorf("ValidateTTY(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name string
		line string
	}{
		{"bare tty", "/dev/pts/3"},
		{"no prefix", "/dev/pts/3" + suffix},
		{"no suffix", prefix + "/dev/pts/3"},
		{"swapped secrets", suffix + "/dev/pts/3" + prefix},
		{"foreign secrets", strings.Repeat("0", len(prefix)) +
			"/dev/pts/3" + strings.Repeat("0", len(suffix))},
		{"empty line", ""},
	} {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			got, err := handshake.ValidateTTY(tc.line)
			if err == nil {
				t.Fatalf("ValidateTTY(%q) = %q, want an error", tc.line, got)
			}
		})
	}
}

func TestTmuxFloatingPane_PopupCommand_pinentryHandshake(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	handshake, err := b.NewPinentryHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewPinentryHandshake: %v", err)
	}
	prefix := handshake.Spec.Env["SEC_PREFIX"]
	suffix := handshake.Spec.Env["SEC_SUFFIX"]

	path, args := b.PopupCommand(handshake.Spec)
	assertPopupCommand(t, path, args, "/usr/bin/tmux", []string{
		"new-pane",
		"-t", "work",
		"-e", "DONE_FIFO_FILE=/tmp/popup/done",
		"-e", "SEC_PREFIX=" + prefix,
		"-e", "SEC_SUFFIX=" + suffix,
		"-e", "TTY_FIFO_FILE=/tmp/popup/tty",
		"--", "echo ${SEC_PREFIX}$(tty)${SEC_SUFFIX} >> ${TTY_FIFO_FILE}" +
			" && read done < ${DONE_FIFO_FILE}",
	})
}

// The guard is the tmux-popup one, shared: same secrets, same validator.
func TestTmuxFloatingPane_ValidateTTY(t *testing.T) {
	handshake, err := tmuxFloatingPaneBackend(t).
		NewPinentryHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewPinentryHandshake: %v", err)
	}
	prefix := handshake.Spec.Env["SEC_PREFIX"]
	suffix := handshake.Spec.Env["SEC_SUFFIX"]

	got, err := handshake.ValidateTTY(prefix + "/dev/pts/3" + suffix)
	if err != nil || got != "/dev/pts/3" {
		t.Errorf("ValidateTTY = %q, %v, want %q", got, err, "/dev/pts/3")
	}
	if _, err := handshake.ValidateTTY("/dev/pts/3"); err == nil {
		t.Error("an unguarded tty must be rejected")
	}
}

// The title is dropped and "--" separates a payload that could start with "-";
// -d must stay away or the popup never takes the keyboard.
func TestTmuxFloatingPane_PopupCommand_argvIsQuoted(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	path, args := b.PopupCommand(runinpopup.PopupSpec{
		Title:   "editor",
		Env:     map[string]string{"B": "2", "A": "1"},
		Command: []string{"vim", "my file.txt"},
	})
	assertPopupCommand(t, path, args, "/usr/bin/tmux", []string{
		"new-pane",
		"-t", "work",
		"-e", "A=1",
		"-e", "B=2",
		"--", `'vim' 'my file.txt'`,
	})
	if slices.Contains(args, "-d") {
		t.Error("-d would leave the focus outside the popup")
	}
}

func TestTmuxFloatingPane_PopupCommand_noSession(t *testing.T) {
	b, err := NewTmuxFloatingPane(Options{TMUX: "/tmp/tmux-1000/default,1,0"})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPane: %v", err)
	}
	path, args := b.PopupCommand(runinpopup.PopupSpec{Command: []string{"true"}})
	assertPopupCommand(t, path, args, "tmux", []string{"new-pane", "--", `'true'`})
}

func TestTmuxFloatingPane_Environ(t *testing.T) {
	meta := "/run/user/1000/tmux-1000/default,111,0"

	if got := tmuxFloatingPaneBackend(t).Environ(); !slices.Equal(got, []string{"TMUX=" + meta}) {
		t.Errorf("Environ = %#v, want TMUX set from the session meta", got)
	}

	inside, err := NewTmuxFloatingPane(Options{SessionMeta: meta, TMUX: "/other,2,0"})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPane: %v", err)
	}
	if got := inside.Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil when $TMUX is already set", got)
	}
}

func TestTmuxAffectedByZoomCrash(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
		why     string
	}{
		{"tmux 3.7b", true, "the version that crashes"},
		{"tmux 3.7b\n", true, "tmux -V output ends in a newline"},
		{"tmux 3.7", true, "no suffix sorts before 3.7a"},
		{"tmux 3.7a", true, ""},
		{"tmux 3.7c", false, "the fix"},
		{"tmux 3.7d", false, ""},
		{"tmux 3.6", true, ""},
		{"tmux 2.9a", true, "floating panes did not exist; the popup fails on its own"},
		{"tmux 3.8", false, ""},
		{"tmux 3.10", false, "the minor is compared as a number, not as text"},
		{"tmux 4.0", false, ""},
		{"tmux next-3.8", true, "a dev build's version pins no commit, so it cannot show the fix"},
		{"tmux master", true, "unparseable"},
		{"3.7c", true, `the "tmux " prefix is part of the contract`},
		{"tmux 3.7.1", true, "not a release form tmux prints"},
		{"tmux 3.", true, "unparseable"},
		{"tmux ", true, "unparseable"},
		{"", true, "no output at all"},
		{"garbage", true, "unparseable"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := tmuxAffectedByZoomCrash(tc.version); got != tc.want {
				t.Errorf("tmuxAffectedByZoomCrash(%q) = %v, want %v (%s)",
					tc.version, got, tc.want, tc.why)
			}
		})
	}
}

func TestZellij_PopupCommand_pinentryHandshake(t *testing.T) {
	b := zellijBackend(t)

	handshake, err := b.NewPinentryHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewPinentryHandshake: %v", err)
	}
	if handshake.ValidateTTY != nil {
		t.Error("zellij announces its tty unguarded; ValidateTTY must stay nil")
	}

	path, args := b.PopupCommand(handshake.Spec)
	assertPopupCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--name=pinentry-curses",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"/bin/bash",
		"-c",
		"echo $(tty) >> /tmp/popup/tty && read done < /tmp/popup/done",
	})
}

func TestZellij_PopupCommand_argvRunsDirectly(t *testing.T) {
	b := zellijBackend(t)

	path, args := b.PopupCommand(runinpopup.PopupSpec{Command: []string{"vim", "my file.txt"}})
	assertPopupCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"vim",
		"my file.txt",
	})
}

func TestZellij_PopupCommand_envGoesThroughShell(t *testing.T) {
	b := zellijBackend(t)

	path, args := b.PopupCommand(runinpopup.PopupSpec{
		Env:     map[string]string{"B": "two", "A": "it's one"},
		Command: []string{"env"},
	})
	assertPopupCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"/bin/bash",
		"-c",
		`export A='it'\''s one'; export B='two'; 'env'`,
	})
}

func TestZellij_Environ(t *testing.T) {
	if got := zellijBackend(t).Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil", got)
	}
}

// Listed explicitly rather than ranging over BackendNames: tmux-floating-pane is
// the one backend whose Prepare does something, and execs tmux to find out.
func TestBackendPrepare_isNoOp(t *testing.T) {
	for _, b := range []runinpopup.Backend{tmuxBackend(t), zellijBackend(t)} {
		t.Run(b.Name(), func(t *testing.T) {
			restore, err := b.Prepare(t.Context())
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if restore != nil {
				t.Error("restore must be nil: neither backend adjusts multiplexer state")
			}
		})
	}
}

func TestNew(t *testing.T) {
	for _, name := range Names() {
		b, err := New(name, Options{
			SessionMeta: "/run/user/1000/tmux-1000/default,111,0",
		})
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if b.Name() != name {
			t.Errorf("New(%q).Name() = %q", name, b.Name())
		}
		if _, ok := b.(runinpopup.PinentryHandshaker); !ok {
			t.Errorf("backend %q does not implement PinentryHandshaker", name)
		}
	}

	_, err := New("tmux", Options{})
	if err == nil {
		t.Fatal("New(\"tmux\") must fail: the name is tmux-popup")
	}
	if !strings.Contains(err.Error(), NameTmuxPopup) {
		t.Errorf("error must list the valid names, got %v", err)
	}
}

func TestDetectName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       string
		tmuxEnv    string
		zellijEnv  string
		want       string
		wantErrStr string
	}{
		{name: "kind wins over env", kind: "TMUX_POPUP", zellijEnv: "0", want: NameTmuxPopup},
		{name: "zellij kind", kind: "ZELLIJ_POPUP", tmuxEnv: "/tmp/s,1,0", want: NameZellij},
		{name: "debug kind", kind: "TMUX_POPUP_DEBUG", want: NameTmuxPopup},
		{
			name: "tmux floating pane kind",
			kind: "TMUX_FLOATING_PANE",
			want: NameTmuxFloatingPane,
		},
		{
			name: "tmux floating pane debug kind",
			kind: "TMUX_FLOATING_PANE_DEBUG",
			want: NameTmuxFloatingPane,
		},
		{
			// The floating backend is opt-in: nothing ambient selects it.
			name:    "bare tmux env stays on display-popup",
			tmuxEnv: "/tmp/s,1,0",
			want:    NameTmuxPopup,
		},
		{name: "lowercase kind", kind: "zellij_popup", want: NameZellij},
		{name: "tmux env", tmuxEnv: "/tmp/s,1,0", want: NameTmuxPopup},
		{name: "zellij env", zellijEnv: "0", want: NameZellij},
		{name: "tmux env wins", tmuxEnv: "/tmp/s,1,0", zellijEnv: "0", want: NameTmuxPopup},
		{name: "unknown kind falls through", kind: "PINENTRY", zellijEnv: "0", want: NameZellij},
		{name: "nothing", wantErrStr: "cannot detect the popup backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectName(tc.kind, tc.tmuxEnv, tc.zellijEnv)
			if tc.wantErrStr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrStr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErrStr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectName: %v", err)
			}
			if got != tc.want {
				t.Errorf("DetectName = %q, want %q", got, tc.want)
			}
		})
	}
}
