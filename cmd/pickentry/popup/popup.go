package popup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	pickflag "github.com/ngicks/run-in-tmux-popup/cmd/pickentry/flag"
	"github.com/ngicks/run-in-tmux-popup/internal/pickentry"
	"golang.org/x/sys/unix"
)

// ErrCanceled is returned when the user cancels the popup without making a selection.
var ErrCanceled = errors.New("popup: selection canceled")

// TmuxOpts holds parsed tmux popup options.
type TmuxOpts struct {
	Position string // center, top, bottom, left, right (empty = tmux default)
	Width    string // e.g., "80%" (empty = tmux default)
	Height   string // e.g., "80%" (empty = tmux default)
}

// validPositions is the set of recognized position keywords.
var validPositions = map[string]bool{
	"center": true,
	"top":    true,
	"bottom": true,
	"left":   true,
	"right":  true,
}

// ParseTmuxOpts parses a tmux options string.
// Format: [position][,WIDTH][,HEIGHT]
// "default" or "" returns all empty (use tmux defaults).
func ParseTmuxOpts(s string) TmuxOpts {
	if s == "" || s == "default" {
		return TmuxOpts{}
	}

	parts := strings.Split(s, ",")
	var opts TmuxOpts
	idx := 0

	// First part: check if it's a position keyword
	if idx < len(parts) && validPositions[parts[idx]] {
		opts.Position = parts[idx]
		idx++
	}

	// Next part(s): width, then height
	if idx < len(parts) {
		opts.Width = parts[idx]
		idx++
	}
	if idx < len(parts) {
		opts.Height = parts[idx]
	}

	return opts
}

// RunInTmuxPopup launches pickentry inside a tmux popup and returns the selected item.
func RunInTmuxPopup(opts TmuxOpts, gf pickflag.Option) (*pickentry.Item, error) {
	// Verify tmux is available
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux not found in PATH: %w", err)
	}

	// Check we're inside tmux
	if os.Getenv("TMUX") == "" {
		return nil, fmt.Errorf("not inside a tmux session (TMUX environment variable not set)")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Create temp dir and FIFO
	tmpDir, err := os.MkdirTemp("", "pickentry-popup-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	resultFifo := tmpDir + "/result"
	if err := unix.Mkfifo(resultFifo, 0o600); err != nil {
		return nil, fmt.Errorf("failed to create FIFO: %w", err)
	}

	// Build inner command args
	innerArgs := reconstructArgs(gf, resultFifo)

	// Build tmux popup command
	tmuxArgs := []string{"popup"}
	if opts.Width != "" {
		tmuxArgs = append(tmuxArgs, "-w", opts.Width)
	}
	if opts.Height != "" {
		tmuxArgs = append(tmuxArgs, "-h", opts.Height)
	}
	tmuxArgs = append(tmuxArgs, "-d", cwd, "-E")
	tmuxArgs = append(tmuxArgs, innerArgs...)

	// Start tmux popup
	cmd := exec.Command(tmuxPath, tmuxArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start tmux popup: %w", err)
	}

	// Read result from FIFO (open O_RDWR so open doesn't block if writer hasn't opened yet)
	resultCh := make(chan *pickentry.Item, 1)
	errCh := make(chan error, 1)
	go func() {
		item, err := readResultFromFifo(resultFifo)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- item
	}()

	// Wait for popup to exit
	cmdErr := cmd.Wait()

	// Give the FIFO reader a moment to finish
	select {
	case item := <-resultCh:
		return item, nil
	case err := <-errCh:
		// If the popup exited with an error and we got a read error, report the popup error
		if cmdErr != nil {
			return nil, ErrCanceled
		}
		return nil, fmt.Errorf("failed to read result: %w", err)
	case <-time.After(500 * time.Millisecond):
		// Timeout: user likely canceled
		if cmdErr != nil {
			return nil, ErrCanceled
		}
		return nil, ErrCanceled
	}
}

// WriteResultToFifo writes a selected item as JSON to the given FIFO path.
func WriteResultToFifo(fifoPath string, item pickentry.Item) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open FIFO for writing: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write to FIFO: %w", err)
	}
	return nil
}

// readResultFromFifo reads a JSON-encoded Item from the FIFO.
func readResultFromFifo(fifoPath string) (*pickentry.Item, error) {
	// O_RDWR so the open doesn't block waiting for a writer
	f, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open FIFO for reading: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		var item pickentry.Item
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("failed to unmarshal result: %w", err)
		}
		return &item, nil
	}

	return nil, ErrCanceled
}

// reconstructArgs builds the inner pickentry command arguments,
// omitting --tmux, --zellij, --no-popup, and --callback, and adding --result-fifo.
func reconstructArgs(gf pickflag.Option, resultFifo string) []string {
	self, err := os.Executable()
	if err != nil {
		// Fallback to os.Args[0]
		self = os.Args[0]
	}

	args := []string{self}

	if gf.ChoicesFile != "" {
		args = append(args, "--choices-file="+gf.ChoicesFile)
	}
	if gf.Choices != "" {
		args = append(args, "--choices="+gf.Choices)
	}
	if gf.Prompt != "" {
		args = append(args, "--prompt="+gf.Prompt)
	}
	if gf.Shell != "" {
		args = append(args, "--shell="+gf.Shell)
	}
	// Omit --callback: the inner process writes JSON to the FIFO;
	// the outer process handles the callback after reading.
	// Omit --tmux, --zellij, --no-popup to prevent recursion.

	args = append(args, "--result-fifo="+resultFifo)

	return args
}
