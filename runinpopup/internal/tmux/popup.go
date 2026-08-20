package tmux

import (
	"context"
	"maps"
	"slices"

	"github.com/ngicks/run-in-tmux-popup/runinpopup/internal/shellargv"
)

// PopupRequest is a display-popup invocation. The popup is a client-side
// overlay, so it targets a client rather than a session.
type PopupRequest struct {
	// ClientId is the tmux client displaying the popup (-c). Empty lets tmux
	// resolve the current one.
	ClientId string
	// Title titles the popup window (-T). Empty leaves tmux's default.
	Title string
	// Env is injected into the popup process (-e).
	Env map[string]string
	// X, Y, Width and Height place and size the popup (-x, -y, -w, -h) in tmux's
	// own vocabulary, so they are passed through verbatim. Empty leaves tmux's
	// default.
	//
	// Both coordinates are display-popup's: -x is the popup's left edge, -y its
	// bottom one. A caller that thinks in top edges converts before it gets here.
	X, Y, Width, Height string
	// Command is the argv the popup runs.
	Command []string
	// Script is a raw shell command line taking precedence over Command.
	Script string
}

// PopupCommand builds "tmux popup -c <client> [-T <title>] [-x <x>] [-y <y>]
// [-w <width>] [-h <height>] [-e KEY=VALUE...] -E <command line>".
// display-popup takes a shell command line, so an argv payload is quoted and
// joined into one.
func (c *Client) PopupCommand(req PopupRequest) (path string, args []string) {
	args = []string{"popup"}
	if req.ClientId != "" {
		args = append(args, "-c", req.ClientId)
	}
	if req.Title != "" {
		args = append(args, "-T", req.Title)
	}
	args = append(args, popupGeometryArgs(req.X, req.Y, req.Width, req.Height)...)
	args = append(args, envArgs(req.Env)...)
	args = append(args, "-E", commandLine(req.Command, req.Script))
	return c.path, args
}

// StartPopup runs PopupCommand's argv. display-popup stays for as long as the
// popup does, so its launcher carries the payload's exit status.
func (c *Client) StartPopup(ctx context.Context, req PopupRequest) (*Launcher, error) {
	_, args := c.PopupCommand(req)
	return c.start(ctx, args)
}

// PaneRequest is a new-pane invocation. A floating pane belongs to a window
// rather than to a client, so it is addressed by session; there is no
// client-targeting flag, because every client viewing the window sees the pane.
type PaneRequest struct {
	// SessionId is the session whose window holds the pane (-t). Empty lets tmux
	// resolve the current one.
	SessionId string
	// Env is injected into the pane process (-e).
	Env map[string]string
	// X, Y, Width and Height place and size the pane, in the same vocabulary
	// display-popup takes and passed through verbatim; the flags they land on are
	// not display-popup's, see NewPaneCommand. Empty leaves tmux's default.
	//
	// X and Y are the pane's top-left corner, which is what new-pane's -X/-Y
	// take — display-popup's bottom-edge -y has no counterpart here.
	X, Y, Width, Height string
	// Command is the argv the pane runs.
	Command []string
	// Script is a raw shell command line taking precedence over Command.
	Script string
}

// NewPaneCommand builds "tmux new-pane [-t <session>] [-X <x>] [-Y <y>]
// [-x <width>] [-y <height>] [-e KEY=VALUE...] -- <command line>".
//
// new-pane can execute an argv directly, but the payload is passed as a single
// shell command line so Script payloads work unchanged; "--" then keeps a
// payload starting with "-" from being read as flags, which display-popup's -E
// does not need.
//
// -d is deliberately absent. It would leave the focus where it was, and a
// passphrase typed into an unfocused popup goes to the pane underneath.
func (c *Client) NewPaneCommand(req PaneRequest) (path string, args []string) {
	args = targeted(req.SessionId, "new-pane")
	args = append(args, paneGeometryArgs(req.X, req.Y, req.Width, req.Height)...)
	args = append(args, envArgs(req.Env)...)
	args = append(args, "--", commandLine(req.Command, req.Script))
	return c.path, args
}

// StartNewPane runs NewPaneCommand's argv. new-pane returns as soon as the pane
// exists, so its launcher says nothing about the payload still running in it.
func (c *Client) StartNewPane(ctx context.Context, req PaneRequest) (*Launcher, error) {
	_, args := c.NewPaneCommand(req)
	return c.start(ctx, args)
}

// popupGeometryArgs renders a popup's placement and size as display-popup's
// flags: position on -x and -y, size on -w and -h.
func popupGeometryArgs(x, y, width, height string) []string {
	return geometryArgs([]geometryFlag{
		{"-x", x}, {"-y", y}, {"-w", width}, {"-h", height},
	})
}

// paneGeometryArgs renders the same placement and size as new-pane's flags,
// which are not display-popup's: on new-pane the lowercase pair is the size and
// the uppercase pair the position, per the usage line this fork reports for it,
//
//	new-pane (newp) [-bdefhIklPvZ] ... [-x width] [-y height] [-X x-position]
//	[-Y y-position] [-t target-pane] [shell-command ...]
//
// so a popup's -w/-h land on -x/-y here and its -x/-y on -X/-Y. Whether the
// position flags take the same single-letter specifiers display-popup does is
// for the tagged integration suite to confirm against a live server; the values
// travel through unchanged either way.
func paneGeometryArgs(x, y, width, height string) []string {
	return geometryArgs([]geometryFlag{
		{"-X", x}, {"-Y", y}, {"-x", width}, {"-y", height},
	})
}

// geometryFlag is one geometry value and the flag carrying it.
type geometryFlag struct{ flag, value string }

// geometryArgs renders the flags a geometry sets, in the given order. An empty
// value emits nothing at all, leaving tmux's own placement.
func geometryArgs(flags []geometryFlag) []string {
	var args []string
	for _, f := range flags {
		if f.value != "" {
			args = append(args, f.flag, f.value)
		}
	}
	return args
}

// envArgs renders an environment as "-e KEY=VALUE" pairs sorted by key, so map
// iteration order never reaches the argv.
func envArgs(env map[string]string) []string {
	var args []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	return args
}

// commandLine renders a payload as the one shell command line both popup
// mechanisms take.
func commandLine(command []string, script string) string {
	if script != "" {
		return script
	}
	return shellargv.Join(command)
}
