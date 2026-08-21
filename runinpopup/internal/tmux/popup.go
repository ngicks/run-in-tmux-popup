package tmux

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	l, err := c.start(ctx, args)
	if err != nil {
		return nil, err
	}
	// Nothing has to be read back from the launcher to close this one: a popup is
	// a client-side overlay and a client has at most one, so the client the launch
	// named is the whole address.
	l.close = func(ctx context.Context) error { return c.ClosePopup(ctx, req.ClientId) }
	return l, nil
}

// ClosePopupCommand builds "tmux popup -C [-c <client>]", the display-popup
// invocation that closes the popup showing on a client, per the usage line this
// fork reports for it,
//
//	display-popup (popup) [-BCEkN] ... [-c target-client] ...
//
// Interrupting the launcher is not this: that only detaches the client the
// launcher itself was, leaving the popup — and the payload in it — running.
func (c *Client) ClosePopupCommand(clientId string) (path string, args []string) {
	args = []string{"popup", "-C"}
	if clientId != "" {
		args = append(args, "-c", clientId)
	}
	return c.path, args
}

// ClosePopup closes the popup a matching StartPopup put on the client.
func (c *Client) ClosePopup(ctx context.Context, clientId string) error {
	_, args := c.ClosePopupCommand(clientId)
	_, err := c.run(ctx, args...)
	return err
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

// paneIdFormat is what new-pane is asked to print about the pane it created.
// The id is the only address a floating pane has out here: the launcher exits
// the moment the pane exists, so without it there would be nothing to name when
// the pane has to be closed again.
const paneIdFormat = "#{pane_id}"

// NewPaneCommand builds "tmux new-pane [-t <session>] -P -F #{pane_id}
// [-X <x>] [-Y <y>] [-x <width>] [-y <height>] [-e KEY=VALUE...]
// -- <command line>".
//
// new-pane can execute an argv directly, but the payload is passed as a single
// shell command line so Script payloads work unchanged; "--" then keeps a
// payload starting with "-" from being read as flags, which display-popup's -E
// does not need.
//
// -P -F is what puts the new pane's id on the launcher's stdout, per the usage
// line this fork reports,
//
//	new-pane (newp) [-bdefhIklPvZ] ... [-F format] ... [-P] ...
//
// which is what KillPane is later given. That the fork prints it where the
// launcher can read it is for the tagged integration suite to confirm against a
// live server.
//
// -d is deliberately absent. It would leave the focus where it was, and a
// passphrase typed into an unfocused popup goes to the pane underneath.
func (c *Client) NewPaneCommand(req PaneRequest) (path string, args []string) {
	args = targeted(req.SessionId, "new-pane")
	args = append(args, "-P", "-F", paneIdFormat)
	args = append(args, paneGeometryArgs(req.X, req.Y, req.Width, req.Height)...)
	args = append(args, envArgs(req.Env)...)
	args = append(args, "--", commandLine(req.Command, req.Script))
	return c.path, args
}

// StartNewPane runs NewPaneCommand's argv. new-pane returns as soon as the pane
// exists, so its launcher says nothing about the payload still running in it.
func (c *Client) StartNewPane(ctx context.Context, req PaneRequest) (*Launcher, error) {
	_, args := c.NewPaneCommand(req)
	l, err := c.start(ctx, args)
	if err != nil {
		return nil, err
	}
	l.close = func(ctx context.Context) error { return c.closePane(ctx, l) }
	return l, nil
}

// closePane closes the floating pane the launcher created. The id it is named by
// is on the launcher's own stdout, so the launcher is waited on first — reading
// those buffers beside a tmux still writing them is the race this waits out, not
// merely a stale read.
func (c *Client) closePane(ctx context.Context, l *Launcher) error {
	if err := l.waitBounded(ctx); err != nil {
		return fmt.Errorf("%s: waiting for the new pane's id: %w", l.line, err)
	}
	paneId, ok := parsePaneId(l.stdout.String())
	if !ok {
		return fmt.Errorf(
			"%s: no pane id to close, the launcher printed %q",
			l.line, strings.TrimSpace(l.stdout.String()+l.stderr.String()),
		)
	}
	return c.KillPane(ctx, paneId)
}

// parsePaneId picks the pane id out of what "new-pane -P -F #{pane_id}"
// printed: tmux names a pane "%<n>" and prints that alone, so output not shaped
// like it is a launcher that never got as far as creating a pane.
func parsePaneId(out string) (string, bool) {
	id, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	id = strings.TrimSpace(id)
	if len(id) < 2 || id[0] != '%' {
		return "", false
	}
	if strings.ContainsFunc(id[1:], func(r rune) bool { return r < '0' || r > '9' }) {
		return "", false
	}
	return id, true
}

// KillPaneCommand builds "tmux kill-pane -t <pane>", per the usage line this
// fork reports for it,
//
//	kill-pane (killp) [-a] [-t target-pane]
//
// A floating pane is a pane: closing it is killing it, and it takes whatever
// runs in it along.
func (c *Client) KillPaneCommand(paneId string) (path string, args []string) {
	return c.path, []string{"kill-pane", "-t", paneId}
}

// KillPane closes the pane, whether it floats or sits in the layout.
func (c *Client) KillPane(ctx context.Context, paneId string) error {
	_, args := c.KillPaneCommand(paneId)
	_, err := c.run(ctx, args...)
	return err
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
