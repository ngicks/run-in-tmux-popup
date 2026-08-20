package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/internal/runworkspace"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/cli"
)

const execLong = `exec runs a command in a terminal-multiplexer popup. The popup runs the command
itself, on the popup's own terminal: its stdin, stdout and stderr are the pane,
so an interactive program simply works there and needs to know nothing about
being run this way.

  run-in-popup exec -- htop

A bridge back to the calling terminal is there for a command that wants it, on
descriptors beside that terminal rather than in place of it. Whatever is piped
into exec is readable on fd 3, everything written to fd 4 is relayed to exec's
own stdout and everything written to fd 5 to its stderr, as it arrives, unaltered
and each stream on its own. TTY_IN, TTY_OUT and TTY_ERR hold the three paths for
a program that cannot inherit descriptors and has to open them by name.

A pipeline through the popup therefore says so in the command:

  find . -type f | run-in-popup exec -- sh -c 'fzf <&3 >&4'

exec exits 0 once the bridge is over: the popup opened and both output streams
ended. It exits 1 when the popup could not be opened, never reached the command,
or a stream could not be relayed. The command's own exit status is not passed on
— only some popup mechanisms carry it back at all, so reporting it would mean a
different answer per backend — and a caller that needs it has to have the
command report it in what it writes.

--backend wins over the configured backend, which in turn wins over
auto-detection from PINENTRY_USER_DATA, then $TMUX (which selects tmux-popup;
tmux floating panes stay an explicit choice), then $ZELLIJ. Everything after
"--" is the command and is passed through unchanged.`

// execWorkspacePrefix names the directory holding one run's stream FIFOs, and
// its debug log when the run has one.
const execWorkspacePrefix = "run-in-popup-exec-"

const execExample = `  run-in-popup exec -- htop
  run-in-popup exec --title build -- go build ./...
  file=$(find . -type f | run-in-popup exec -- sh -c 'fzf <&3 >&4')`

func execCmd(parent *cobra.Command, flagConfig *string) {
	var (
		flagBackend string
		flagTitle   string
	)

	cmd := &cobra.Command{
		Use:     "exec [flags] -- command [arg...]",
		Short:   "Run a command on a popup's own terminal, bridged back here on fd 3/4/5",
		Long:    execLong,
		Example: execExample,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args, *flagConfig, flagBackend, flagTitle)
		},
	}

	cmd.Flags().StringVar(
		&flagBackend,
		"backend",
		"",
		fmt.Sprintf("popup backend, %s (default: auto-detected)", cli.BackendNameList()),
	)
	cmd.Flags().StringVar(
		&flagTitle,
		"title",
		"",
		"popup title (default: the backend's own;"+
			" tmux-floating-pane has no title flag and ignores it)",
	)

	parent.AddCommand(cmd)
}

func runExec(
	cmd *cobra.Command,
	args []string,
	flagConfig, flagBackend, flagTitle string,
) (err error) {
	ctx := cmd.Context()

	command, err := execCommandArgs(cmd, args)
	if err != nil {
		return err
	}

	cfg, err := runinpopup.LoadConfig(flagConfig)
	if err != nil {
		return err
	}

	rt, err := resolveRuntime(runtimeInputs{
		Config:    cfg,
		Overrides: execFlagOverrides(cmd, flagBackend),
	}, os.Environ())
	if err != nil {
		return err
	}

	workspace, err := runworkspace.Open(
		execWorkspacePrefix,
		rt.UserData.Debug(),
		contextkey.ValueSlogLoggerFallback(ctx, slog.Default()),
	)
	if err != nil {
		return err
	}
	defer func() {
		// A debug log that would not close is worth reporting, but never worth
		// hiding how the bridge itself went.
		if cerr := workspace.Close(); err == nil {
			err = cerr
		}
	}()

	popup := &runinpopup.PopupLauncher{
		Backend:   rt.Backend,
		Logger:    workspace.Logger,
		Workspace: workspace.Options,
	}
	// The launch closes every endpoint it is handed once that stream ends, and
	// these three are this process's own, handed to it by whoever ran it — so they
	// go in behind ends that ignore being closed.
	return execBridge(
		ctx,
		popup,
		runinpopup.PopupSpec{Title: flagTitle, Command: command},
		io.NopCloser(os.Stdin),
		unclosableWriter{os.Stdout},
		unclosableWriter{os.Stderr},
	)
}

// execBridge runs spec in a popup with this process's three streams reaching it
// beside its own, and returns once what the command wrote to fd 4 and fd 5 has
// arrived. The input relay is not waited on: it sits in a read on this process's
// stdin, which the popup being over says nothing about.
func execBridge(
	ctx context.Context,
	popup *runinpopup.PopupLauncher,
	spec runinpopup.PopupSpec,
	stdin io.ReadCloser,
	stdout, stderr io.WriteCloser,
) error {
	command, err := popup.Exec(ctx, spec, runinpopup.PopupStreams{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		// The popup's terminal is the command's, so what the user runs draws there
		// as it would anywhere; the caller's streams are the side channel.
		KeepStdio: true,
	})
	if err != nil {
		return err
	}
	return command.WaitStreams()
}

// unclosableWriter hands a writer out to something that closes what it is given;
// io.NopCloser is the same guard on the reading side.
type unclosableWriter struct{ io.Writer }

func (unclosableWriter) Close() error { return nil }

// execCommandArgs picks the command out of a parsed invocation: everything after
// "--". Without a "--" the bare positional arguments are taken instead, which
// works for a command carrying no flags of its own; anything else needs the
// separator, or pflag claims the flags.
func execCommandArgs(cmd *cobra.Command, args []string) ([]string, error) {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		args = args[dash:]
	}
	if len(args) == 0 {
		return nil, errors.New(
			`no command to run: pass one after "--", e.g. run-in-popup exec -- make test`,
		)
	}
	return args, nil
}

// execFlagOverrides turns explicitly-set flags into the topmost config layer; a
// flag left alone stays absent from the partial, so the file and environment
// layers keep their say. --title is not here: the popup title is a property of
// one run, not configuration.
func execFlagOverrides(cmd *cobra.Command, backend string) runinpopup.PartialConfig {
	var p runinpopup.PartialConfig
	if cmd.Flags().Changed("backend") {
		p.Backend = &backend
	}
	return p
}
