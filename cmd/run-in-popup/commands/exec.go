package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"time"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/cli"
)

const execLong = `exec runs a command in a terminal-multiplexer popup and reports what it did to
the process that called it. The command owns the popup while it runs — its
output is drawn there live and its stdin is the popup's terminal, so it may
prompt — and once it exits, this process prints a single compact JSON object on
its stdout:

  {"command":["make","test"],"exit_code":1,"stdout":"...","stderr":"..."}

stdout and stderr hold everything the command wrote. "error" is filled instead,
with exit_code -1, when there is no status to report: the command never started,
or its output could not be relayed. exec itself exits 0 whenever the exchange
worked — the command's own status is exit_code, not this process's.

--backend wins over the configured default_backend, which in turn wins over
auto-detection from PINENTRY_USER_DATA, then $TMUX (which selects tmux-popup;
tmux floating panes stay an explicit choice), then $ZELLIJ. Everything after
"--" is the command and is passed through unchanged.`

const execExample = `  run-in-popup exec -- make test
  run-in-popup exec --title build -- go build ./...
  run-in-popup exec --backend tmux-floating-pane -- git rebase -i main`

func execCmd(parent *cobra.Command, flagConfig *string) {
	var (
		flagBackend string
		flagTitle   string
	)

	cmd := &cobra.Command{
		Use:     "exec [flags] -- command [arg...]",
		Short:   "Run a command in a popup and print its result as JSON",
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
		`popup backend, "tmux-popup", "tmux-floating-pane" or "zellij"`+
			` (default: auto-detected)`,
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
) error {
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

	payloadPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating this executable for the exec payload: %w", err)
	}

	popup := &runinpopup.PopupLauncher{
		Backend: rt.Backend,
		Logger:  contextkey.ValueSlogLoggerFallback(ctx, slog.Default()),
		Workspace: runinpopup.WorkspaceOptions{
			NamePrefix: "run-in-popup-exec-",
			Retain:     rt.UserData.Debug(),
		},
	}
	exchange := &runinpopup.JsonIpcLauncher[[]string, runinpopup.ExecResult]{
		Popup:       popup,
		PartialSpec: runinpopup.PopupSpec{Title: flagTitle},
		AddPayload: func(command []string, spec runinpopup.PopupSpec) runinpopup.PopupSpec {
			spec.Command = execPayloadArgv(payloadPath, popup.StartupTimeout, command)
			return spec
		},
	}

	conn, err := exchange.Exec(ctx, command)
	if err != nil {
		return err
	}
	result, reported := <-conn.Results()
	// The payload answers once, but the stream still has to be drained: the
	// exchange has not ended while anything is left on it.
	for range conn.Results() {
	}
	if err := conn.Wait(); err != nil {
		return err
	}
	if !reported {
		return errors.New("the popup payload exited without reporting a result")
	}
	return cli.RenderExecResult(cmd.OutOrStdout(), result)
}

// execPayloadArgv builds the popup's argv: the payload subcommand, the bound
// both halves give the rendezvous, and the user's command behind the "--" that
// separates it from them. The bound only appears when it is not the one the
// payload would pick anyway, so the ordinary popup command line — the one that
// shows up in ps — stays as short as it was.
func execPayloadArgv(payloadPath string, startupTimeout time.Duration, command []string) []string {
	argv := []string{payloadPath, runinpopup.ExecPayloadCommandName}
	if startupTimeout != 0 {
		argv = append(argv, fmt.Sprintf(
			"--%s=%s", runinpopup.ExecPayloadStartupTimeoutFlag, startupTimeout,
		))
	}
	return slices.Concat(argv, []string{"--"}, command)
}

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
		p.DefaultBackend = &backend
	}
	return p
}
