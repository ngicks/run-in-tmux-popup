package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	pickflag "github.com/ngicks/run-in-tmux-popup/cmd/pickentry/flag"
	"github.com/ngicks/run-in-tmux-popup/cmd/pickentry/popup"
	"github.com/ngicks/run-in-tmux-popup/internal/pickentry"
)

func main() {
	pickflag.Parse()
	gf := pickflag.GlobalOption

	// Popup dispatch
	if !gf.NoPopup {
		if gf.Tmux != "" {
			selected, err := popup.RunInTmuxPopup(popup.ParseTmuxOpts(gf.Tmux), gf)
			if err != nil {
				if errors.Is(err, popup.ErrCanceled) {
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			handleResult(selected, gf)
			return
		}
		if gf.Zellij != "" {
			fmt.Fprintf(os.Stderr, "Error: --zellij is not yet implemented\n")
			os.Exit(1)
		}
	}

	// Load items
	var items pickentry.Items
	var err error

	if gf.ChoicesFile != "" {
		items, err = pickentry.LoadItemsFromFile(gf.ChoicesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading items from file: %v\n", err)
			os.Exit(1)
		}
	} else if gf.Choices != "" {
		items, err = pickentry.ParseItemsFromString(gf.Choices)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing items: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Error: must provide either --choices-file or --choices\n\n")
		pickflag.Usage()
		os.Exit(1)
	}

	// Create model
	model := pickentry.NewModel(items, gf.Prompt)

	// Run tea program
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}

	// Handle result
	m := finalModel.(pickentry.Model)
	if m.Selected == nil {
		// User quit without selection
		os.Exit(1)
	}

	// If --result-fifo is set, write result to FIFO instead of normal output
	if gf.ResultFifo != "" {
		if err := popup.WriteResultToFifo(gf.ResultFifo, *m.Selected); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing result to FIFO: %v\n", err)
			os.Exit(1)
		}
		return
	}

	handleResult(m.Selected, gf)
}

// handleResult executes the callback or prints the command to stdout.
func handleResult(selected *pickentry.Item, gf pickflag.Option) {
	if gf.Callback != "" {
		// Render template
		rendered, err := pickentry.RenderCallback(gf.Callback, *selected)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error rendering callback: %v\n", err)
			os.Exit(1)
		}

		// Execute in shell
		shellCmd := pickentry.DetectShell(gf.Shell)
		if err := pickentry.ExecuteCallback(shellCmd, rendered); err != nil {
			fmt.Fprintf(os.Stderr, "Error executing callback: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Print cmd to stdout
		fmt.Println(selected.Cmd)
	}
}
