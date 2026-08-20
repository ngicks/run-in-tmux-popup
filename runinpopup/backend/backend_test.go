package backend

import (
	"os"
	"path/filepath"
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
		X:       spec.X,
		Y:       spec.Y,
		Width:   spec.Width,
		Height:  spec.Height,
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
	if handshake.ValidateTTY != nil {
		t.Error("ValidateTTY must be nil: the announced tty is taken as-is")
	}

	req, err := b.popupRequest(launchSpec(handshake.Spec))
	if err != nil {
		t.Fatalf("popupRequest: %v", err)
	}
	path, args := b.tmux.PopupCommand(req)
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
		"popup",
		"-c", "%1",
		"-e", "DONE_FIFO_FILE=/tmp/popup/done",
		"-e", "TTY_FIFO_FILE=/tmp/popup/tty",
		"-E", "echo $(tty) >> ${TTY_FIFO_FILE}" +
			" && read done < ${DONE_FIFO_FILE}",
	})
}

// The spec's geometry reaches display-popup as the flags it names, and Y as the
// coordinate display-popup means by one: a spec places the popup's top-left
// corner, display-popup's -y its bottom edge, so the height is added on the way
// through. Whether the sum lands where it was asked for at the edge of the
// terminal is tmux's answer to give — it clamps a popup itself, and nothing here
// second-guesses the bounds.
func TestTmuxPopup_Launch_geometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec runinpopup.PopupSpec
		// want is the geometry between the fixed head of the command line and the
		// payload at its end.
		want []string
	}{
		{
			name: "cells are added",
			spec: runinpopup.PopupSpec{X: "C", Y: "10", Width: "80%", Height: "20"},
			want: []string{"-x", "C", "-y", "30", "-w", "80%", "-h", "20"},
		},
		{
			name: "percentages are added as percentages",
			spec: runinpopup.PopupSpec{Y: "10%", Height: "40%"},
			want: []string{"-y", "50%", "-h", "40%"},
		},
		{
			// The top row is a row like any other: it is the height that reaches -y.
			name: "the top row",
			spec: runinpopup.PopupSpec{Y: "0", Height: "20"},
			want: []string{"-y", "20", "-h", "20"},
		},
		{
			// A specifier names a placement tmux works out with the height in hand,
			// so there is nothing to add to it — and nothing to demand beside it.
			name: "a position specifier passes through",
			spec: runinpopup.PopupSpec{Y: "C", Height: "20"},
			want: []string{"-y", "C", "-h", "20"},
		},
		{
			name: "a position specifier needs no height at all",
			spec: runinpopup.PopupSpec{Y: "S"},
			want: []string{"-y", "S"},
		},
		{
			// An empty Y is no coordinate to convert, and a height alone is tmux's
			// own placement at that size.
			name: "no Y, no conversion",
			spec: runinpopup.PopupSpec{Height: "20"},
			want: []string{"-h", "20"},
		},
		{
			// X is the left edge on every mechanism; only Y ever disagreed.
			name: "X travels as it was written",
			spec: runinpopup.PopupSpec{X: "10"},
			want: []string{"-x", "10"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tmuxBackend(t)
			tc.spec.Command = []string{"htop"}

			req, err := b.popupRequest(launchSpec(tc.spec))
			if err != nil {
				t.Fatalf("popupRequest: %v", err)
			}
			path, args := b.tmux.PopupCommand(req)

			assertCommand(t, path, args, "/usr/bin/tmux", slices.Concat(
				[]string{"popup", "-c", "%1"},
				tc.want,
				[]string{"-E", `'htop'`},
			))
		})
	}
}

// Adding a height to Y is the only way display-popup can be asked for a top
// edge, so a Y that names one without a height to add — or with a height
// measured in the other unit — is refused rather than placed at a guess.
func TestTmuxPopup_Launch_yNeedsAMatchingHeight(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec runinpopup.PopupSpec
	}{
		{name: "cells without a height", spec: runinpopup.PopupSpec{Y: "10"}},
		{name: "a percentage without a height", spec: runinpopup.PopupSpec{Y: "10%"}},
		{
			name: "cells against a percentage",
			spec: runinpopup.PopupSpec{Y: "10", Height: "40%"},
		},
		{
			name: "a percentage against cells",
			spec: runinpopup.PopupSpec{Y: "10%", Height: "20"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Command = []string{"htop"}

			_, err := tmuxBackend(t).popupRequest(launchSpec(tc.spec))
			if err == nil {
				t.Fatal("popupRequest must fail: the top edge cannot be placed")
			}
			if !strings.Contains(err.Error(), NameTmuxPopup) {
				t.Errorf("err = %v, want the backend named in it", err)
			}
			if !strings.Contains(err.Error(), "Height in the same unit") {
				t.Errorf("err = %v, want it to name what is missing", err)
			}
			if !strings.Contains(err.Error(), tc.spec.Y) {
				t.Errorf("err = %v, want the value %q quoted in it", err, tc.spec.Y)
			}
		})
	}
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

func TestTmuxFloatingPane_Launch_ttyHandshake(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	handshake, err := b.NewTTYHandshake("/tmp/popup/tty", "/tmp/popup/done")
	if err != nil {
		t.Fatalf("NewTTYHandshake: %v", err)
	}
	if handshake.ValidateTTY != nil {
		t.Error("ValidateTTY must be nil: the announced tty is taken as-is")
	}

	path, args := b.tmux.NewPaneCommand(b.paneRequest(launchSpec(handshake.Spec)))
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
		"new-pane",
		"-t", "work",
		"-e", "DONE_FIFO_FILE=/tmp/popup/done",
		"-e", "TTY_FIFO_FILE=/tmp/popup/tty",
		"--", "echo $(tty) >> ${TTY_FIFO_FILE}" +
			" && read done < ${DONE_FIFO_FILE}",
	})
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

// The same geometry, on the flags new-pane has for it: the size on -x/-y and
// the position on -X/-Y, which is not how display-popup spells either.
func TestTmuxFloatingPane_Launch_geometry(t *testing.T) {
	b := tmuxFloatingPaneBackend(t)

	path, args := b.tmux.NewPaneCommand(b.paneRequest(launchSpec(runinpopup.PopupSpec{
		X:       "C",
		Y:       "10",
		Width:   "80%",
		Height:  "20",
		Command: []string{"htop"},
	})))
	assertCommand(t, path, args, "/usr/bin/tmux", []string{
		"new-pane",
		"-t", "work",
		"-X", "C",
		"-Y", "10",
		"-x", "80%",
		"-y", "20",
		"--", `'htop'`,
	})
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

	req, err := b.runRequest(launchSpec(handshake.Spec))
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}
	path, args := b.zellij.RunCommand(req)
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

// The environment is what a "zellij run" argv must not carry: it lands in the
// launch's work directory instead, and the command line names only the file the
// payload sources.
func TestZellij_Launch_envGoesToAWorkDirFile(t *testing.T) {
	b := zellijBackend(t)
	dir := t.TempDir()

	req, err := b.runRequest(runinpopup.LaunchSpec{
		Title:   "editor",
		Env:     map[string]string{"B": "two", "A": "it's one"},
		Command: []string{"vim", "my file.txt"},
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}

	envFile := filepath.Join(dir, "env")
	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("reading the env file: %v", err)
	}
	if want := "export A='it'\\''s one'\nexport B='two'\n"; string(content) != want {
		t.Errorf("env file =\n\t%q\nwant\n\t%q", content, want)
	}

	path, args := b.zellij.RunCommand(req)
	assertCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--name=editor",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"/bin/bash",
		"-c",
		". '" + envFile + "' && { 'vim' 'my file.txt'\n}",
	})
	for _, arg := range args {
		if strings.Contains(arg, "it's one") {
			t.Fatalf("args = %#v, want no environment value in them", args)
		}
	}
}

// Cells and percentages are as much zellij's vocabulary as tmux's, so they
// travel the whole way.
func TestZellij_Launch_geometry(t *testing.T) {
	b := zellijBackend(t)

	req, err := b.runRequest(launchSpec(runinpopup.PopupSpec{
		X:       "10",
		Y:       "5%",
		Width:   "80%",
		Height:  "20",
		Command: []string{"htop"},
	}))
	if err != nil {
		t.Fatalf("runRequest: %v", err)
	}
	path, args := b.zellij.RunCommand(req)
	assertCommand(t, path, args, "/usr/bin/zellij", []string{
		"--session=session-id",
		"run",
		"--x=10",
		"--y=5%",
		"--width=80%",
		"--height=20",
		"--floating",
		"--close-on-exit",
		"--pinned=true",
		"--",
		"htop",
	})
}

// A tmux position specifier has no zellij equivalent, and placing the pane
// somewhere else instead would be a silent answer to a request nobody can
// honor. Both position fields have to say so, and say which value they mean.
func TestZellij_Launch_rejectsTmuxPositions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  runinpopup.PopupSpec
		value string
	}{
		{name: "x", spec: runinpopup.PopupSpec{X: "C"}, value: "C"},
		{name: "y", spec: runinpopup.PopupSpec{Y: "M"}, value: "M"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Command = []string{"htop"}

			_, err := zellijBackend(t).runRequest(launchSpec(tc.spec))
			if err == nil {
				t.Fatal("runRequest must fail: zellij cannot be asked for a tmux position")
			}
			if !strings.Contains(err.Error(), NameZellij) {
				t.Errorf("err = %v, want the backend named in it", err)
			}
			if !strings.Contains(err.Error(), tc.value) {
				t.Errorf("err = %v, want the value %q quoted in it", err, tc.value)
			}
		})
	}
}

// The file has nowhere to go without the directory, and a launch that carries an
// environment is given one; running the payload without its environment would
// be the worse answer.
func TestZellij_Launch_envNeedsAWorkDir(t *testing.T) {
	_, err := zellijBackend(t).runRequest(runinpopup.LaunchSpec{
		Env:     map[string]string{"A": "one"},
		Command: []string{"true"},
	})
	if err == nil {
		t.Fatal("runRequest must fail: there is nowhere to write the environment")
	}
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
