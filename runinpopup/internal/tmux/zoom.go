package tmux

import (
	"context"
	"fmt"
	"strings"
)

// ZoomStateFormat asks for both halves of the answer in one round trip: whether
// the window is zoomed, and which pane holds the zoom.
const ZoomStateFormat = "#{window_zoomed_flag}:#{pane_id}"

// ZoomedPane reports whether the targeted session's window has a zoomed pane,
// and which one.
func (c *Client) ZoomedPane(ctx context.Context, sessionId string) (bool, string, error) {
	out, err := c.run(ctx, append(targeted(sessionId, "display-message", "-p"), ZoomStateFormat)...)
	if err != nil {
		return false, "", fmt.Errorf("querying the zoomed pane: %w", err)
	}
	flag, paneId, ok := strings.Cut(strings.TrimSpace(out), ":")
	if !ok || paneId == "" {
		return false, "", fmt.Errorf("querying the zoomed pane: unexpected output %q", out)
	}
	return flag == "1", paneId, nil
}

// ToggleZoom zooms the pane, or de-zooms it when it is the zoomed one.
func (c *Client) ToggleZoom(ctx context.Context, paneId string) error {
	_, err := c.run(ctx, "resize-pane", "-Z", "-t", paneId)
	return err
}
