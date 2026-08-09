package runinpopup

import (
	"slices"
	"strings"
	"testing"
)

func tmuxBackend(t *testing.T) *TmuxPopupBackend {
	t.Helper()
	b, err := NewTmuxPopupBackend(BackendOptions{
		BinaryPath:  "/usr/bin/tmux",
		ClientId:    "%1",
		SessionMeta: "/run/user/1000/tmux-1000/default,111,0",
	})
	if err != nil {
		t.Fatalf("NewTmuxPopupBackend: %v", err)
	}
	return b
}

func tmuxFloatingPaneBackend(t *testing.T) *TmuxFloatingPaneBackend {
	t.Helper()
	b, err := NewTmuxFloatingPaneBackend(BackendOptions{
		BinaryPath:  "/usr/bin/tmux",
		SessionId:   "work",
		ClientId:    "%1",
		SessionMeta: "/run/user/1000/tmux-1000/default,111,0",
	})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPaneBackend: %v", err)
	}
	return b
}

func zellijBackend(t *testing.T) *ZellijBackend {
	t.Helper()
	b, err := NewZellijBackend(BackendOptions{
		BinaryPath: "/usr/bin/zellij",
		SessionId:  "session-id",
		Shell:      "/bin/bash",
	})
	if err != nil {
		t.Fatalf("NewZellijBackend: %v", err)
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
func TestTmuxPopupBackend_PopupCommand_pinentryHandshake(t *testing.T) {
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

func TestTmuxPopupBackend_PopupCommand_argvIsQuoted(t *testing.T) {
	b := tmuxBackend(t)

	path, args := b.PopupCommand(PopupSpec{
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

func TestTmuxPopupBackend_PopupCommand_noClient(t *testing.T) {
	b, err := NewTmuxPopupBackend(BackendOptions{TMUX: "/tmp/tmux-1000/default,1,0"})
	if err != nil {
		t.Fatalf("NewTmuxPopupBackend: %v", err)
	}
	path, args := b.PopupCommand(PopupSpec{Command: []string{"true"}})
	assertPopupCommand(t, path, args, "tmux", []string{"popup", "-E", `'true'`})
}

func TestNewTmuxPopupBackend_sessionMeta(t *testing.T) {
	// Malformed meta only matters when it is the value that will be exported.
	if _, err := NewTmuxPopupBackend(BackendOptions{SessionMeta: "not-a-meta"}); err == nil {
		t.Error("malformed session meta must be rejected when $TMUX is unset")
	}
	if _, err := NewTmuxPopupBackend(BackendOptions{
		SessionMeta: "not-a-meta",
		TMUX:        "/tmp/tmux-1000/default,1,0",
	}); err != nil {
		t.Errorf("session meta is unused when $TMUX is set, got %v", err)
	}
}

func TestTmuxPopupBackend_Environ(t *testing.T) {
	meta := "/run/user/1000/tmux-1000/default,111,0"

	b := tmuxBackend(t)
	if got := b.Environ(); !slices.Equal(got, []string{"TMUX=" + meta}) {
		t.Errorf("Environ = %#v, want TMUX set from the session meta", got)
	}

	inside, err := NewTmuxPopupBackend(BackendOptions{SessionMeta: meta, TMUX: "/other,2,0"})
	if err != nil {
		t.Fatalf("NewTmuxPopupBackend: %v", err)
	}
	if got := inside.Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil when $TMUX is already set", got)
	}
}

func TestTmuxPopupBackend_ValidateTTY(t *testing.T) {
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

func TestTmuxFloatingPaneBackend_PopupCommand_pinentryHandshake(t *testing.T) {
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
func TestTmuxFloatingPaneBackend_ValidateTTY(t *testing.T) {
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
func TestTmuxFloatingPaneBackend_PopupCommand_argvIsQuoted(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	path, args := b.PopupCommand(PopupSpec{
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

func TestTmuxFloatingPaneBackend_PopupCommand_noSession(t *testing.T) {
	b, err := NewTmuxFloatingPaneBackend(BackendOptions{TMUX: "/tmp/tmux-1000/default,1,0"})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPaneBackend: %v", err)
	}
	path, args := b.PopupCommand(PopupSpec{Command: []string{"true"}})
	assertPopupCommand(t, path, args, "tmux", []string{"new-pane", "--", `'true'`})
}

func TestTmuxFloatingPaneBackend_Environ(t *testing.T) {
	meta := "/run/user/1000/tmux-1000/default,111,0"

	if got := tmuxFloatingPaneBackend(t).Environ(); !slices.Equal(got, []string{"TMUX=" + meta}) {
		t.Errorf("Environ = %#v, want TMUX set from the session meta", got)
	}

	inside, err := NewTmuxFloatingPaneBackend(BackendOptions{SessionMeta: meta, TMUX: "/other,2,0"})
	if err != nil {
		t.Fatalf("NewTmuxFloatingPaneBackend: %v", err)
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

func TestZellijBackend_PopupCommand_pinentryHandshake(t *testing.T) {
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

func TestZellijBackend_PopupCommand_argvRunsDirectly(t *testing.T) {
	b := zellijBackend(t)

	path, args := b.PopupCommand(PopupSpec{Command: []string{"vim", "my file.txt"}})
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

func TestZellijBackend_PopupCommand_envGoesThroughShell(t *testing.T) {
	b := zellijBackend(t)

	path, args := b.PopupCommand(PopupSpec{
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

func TestZellijBackend_Environ(t *testing.T) {
	if got := zellijBackend(t).Environ(); got != nil {
		t.Errorf("Environ = %#v, want nil", got)
	}
}

// Listed explicitly rather than ranging over BackendNames: tmux-floating-pane is
// the one backend whose Prepare does something, and execs tmux to find out.
func TestBackendPrepare_isNoOp(t *testing.T) {
	for _, b := range []Backend{tmuxBackend(t), zellijBackend(t)} {
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

func TestNewBackend(t *testing.T) {
	for _, name := range BackendNames() {
		b, err := NewBackend(name, BackendOptions{
			SessionMeta: "/run/user/1000/tmux-1000/default,111,0",
		})
		if err != nil {
			t.Fatalf("NewBackend(%q): %v", name, err)
		}
		if b.Name() != name {
			t.Errorf("NewBackend(%q).Name() = %q", name, b.Name())
		}
		if _, ok := b.(PinentryHandshaker); !ok {
			t.Errorf("backend %q does not implement PinentryHandshaker", name)
		}
	}

	_, err := NewBackend("tmux", BackendOptions{})
	if err == nil {
		t.Fatal("NewBackend(\"tmux\") must fail: the name is tmux-popup")
	}
	if !strings.Contains(err.Error(), BackendTmuxPopup) {
		t.Errorf("error must list the valid names, got %v", err)
	}
}

func TestDetectBackendName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       string
		tmuxEnv    string
		zellijEnv  string
		want       string
		wantErrStr string
	}{
		{name: "kind wins over env", kind: "TMUX_POPUP", zellijEnv: "0", want: BackendTmuxPopup},
		{name: "zellij kind", kind: "ZELLIJ_POPUP", tmuxEnv: "/tmp/s,1,0", want: BackendZellij},
		{name: "debug kind", kind: "TMUX_POPUP_DEBUG", want: BackendTmuxPopup},
		{
			name: "tmux floating pane kind",
			kind: "TMUX_FLOATING_PANE",
			want: BackendTmuxFloatingPane,
		},
		{
			name: "tmux floating pane debug kind",
			kind: "TMUX_FLOATING_PANE_DEBUG",
			want: BackendTmuxFloatingPane,
		},
		{
			// The floating backend is opt-in: nothing ambient selects it.
			name:    "bare tmux env stays on display-popup",
			tmuxEnv: "/tmp/s,1,0",
			want:    BackendTmuxPopup,
		},
		{name: "lowercase kind", kind: "zellij_popup", want: BackendZellij},
		{name: "tmux env", tmuxEnv: "/tmp/s,1,0", want: BackendTmuxPopup},
		{name: "zellij env", zellijEnv: "0", want: BackendZellij},
		{name: "tmux env wins", tmuxEnv: "/tmp/s,1,0", zellijEnv: "0", want: BackendTmuxPopup},
		{name: "unknown kind falls through", kind: "PINENTRY", zellijEnv: "0", want: BackendZellij},
		{name: "nothing", wantErrStr: "cannot detect the popup backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectBackendName(tc.kind, tc.tmuxEnv, tc.zellijEnv)
			if tc.wantErrStr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrStr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErrStr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectBackendName: %v", err)
			}
			if got != tc.want {
				t.Errorf("DetectBackendName = %q, want %q", got, tc.want)
			}
		})
	}
}
