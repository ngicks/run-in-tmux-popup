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

func assertCommand(
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

// launchSpec is what the launch layer hands a backend for a payload that needs
// no stream of its own: the template's command line, unchanged.
func launchSpec(spec runinpopup.PopupSpec) runinpopup.LaunchSpec {
	return runinpopup.LaunchSpec{
		Title:   spec.Title,
		Env:     spec.Env,
		Command: spec.Command,
		Script:  spec.Script,
	}
}

// The handshake argv is asserted literally: it is the one command line proven to
// work against a live tmux, so any change to it must be a deliberate edit here.
func TestTmuxPopup_Launch_ttyHandshake(t *testing.T) {
	b := tmuxBackend(t)

	handshake, err := b.NewTTYHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewTTYHandshake: %v", err)
	}
	prefix := handshake.Spec.Env["SEC_PREFIX"]
	suffix := handshake.Spec.Env["SEC_SUFFIX"]

	path, args := b.tmux.PopupCommand(b.popupRequest(launchSpec(handshake.Spec)))
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
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

// The session meta rules are the tmux client's; both constructors have to
// surface its verdict.
func TestNewTmuxBackends_sessionMetaIsValidated(t *testing.T) {
	if _, err := NewTmuxPopup(Options{SessionMeta: "not-a-meta"}); err == nil {
		t.Error("NewTmuxPopup must reject a malformed session meta")
	}
	if _, err := NewTmuxFloatingPane(Options{SessionMeta: "not-a-meta"}); err == nil {
		t.Error("NewTmuxFloatingPane must reject a malformed session meta")
	}
}

// The session environ is what points a launch at the server hosting the popup,
// so both backends have to have handed their client the session meta.
func TestTmuxBackends_sessionEnviron(t *testing.T) {
	want := []string{"TMUX=/run/user/1000/tmux-1000/default,111,0"}

	if got := tmuxBackend(t).tmux.Environ(); !slices.Equal(got, want) {
		t.Errorf("TmuxPopup client environ = %#v, want %#v", got, want)
	}
	if got := tmuxFloatingPaneBackend(t).tmux.Environ(); !slices.Equal(got, want) {
		t.Errorf("TmuxFloatingPane client environ = %#v, want %#v", got, want)
	}
}

func TestTmuxPopup_ValidateTTY(t *testing.T) {
	b := tmuxBackend(t)

	handshake, err := b.NewTTYHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewTTYHandshake: %v", err)
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

func TestTmuxFloatingPane_Launch_ttyHandshake(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	handshake, err := b.NewTTYHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewTTYHandshake: %v", err)
	}
	prefix := handshake.Spec.Env["SEC_PREFIX"]
	suffix := handshake.Spec.Env["SEC_SUFFIX"]

	path, args := b.tmux.NewPaneCommand(b.paneRequest(launchSpec(handshake.Spec)))
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
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
		NewTTYHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewTTYHandshake: %v", err)
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

// new-pane has no title flag, so the spec's title has nowhere to go.
func TestTmuxFloatingPane_Launch_dropsTitle(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	_, args := b.tmux.NewPaneCommand(b.paneRequest(runinpopup.LaunchSpec{
		Title:   "editor",
		Command: []string{"true"},
	}))
	if slices.Contains(args, "-T") || slices.Contains(args, "editor") {
		t.Errorf("args = %#v, want the title dropped", args)
	}
}

func TestZellij_Launch_ttyHandshake(t *testing.T) {
	b := zellijBackend(t)

	handshake, err := b.NewTTYHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewTTYHandshake: %v", err)
	}
	if handshake.ValidateTTY != nil {
		t.Error("zellij announces its tty unguarded; ValidateTTY must stay nil")
	}

	path, args := b.zellij.RunCommand(b.runRequest(launchSpec(handshake.Spec)))
	assertCommand(t, path, args, "/usr/bin/zellij", []string{
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

// Listed explicitly rather than ranging over Names: tmux-floating-pane is
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
		if _, ok := b.(runinpopup.TTYHandshaker); !ok {
			t.Errorf("backend %q does not implement TTYHandshaker", name)
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
