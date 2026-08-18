package commands

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/internal/runworkspace"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/cli"
)

const pinentryLong = `pinentry proxies the Assuan exchange gpg-agent runs over stdin/stdout to a
pinentry process drawing in a terminal-multiplexer popup: it opens the popup,
learns the tty it runs on and rewrites the "OPTION ttyname=" line so the prompt
appears there instead of on whichever terminal gpg-agent picked.

It is meant to be invoked by gpg-agent through a wrapper script that exports
PINENTRY_USER_DATA, whose fields locate the multiplexer:

  KIND:multiplexer_path:session_id:client_id:session_meta

KIND is "TMUX_POPUP", "TMUX_FLOATING_PANE" or "ZELLIJ_POPUP"; a "_DEBUG" suffix
additionally writes a debug log to log.txt in the temporary directory and keeps
that directory around.

--backend wins over the configured default_backend, which in turn wins over
auto-detection from PINENTRY_USER_DATA, then $TMUX (which selects tmux-popup;
tmux floating panes stay an explicit choice), then $ZELLIJ. Arguments after "--"
are passed to the pinentry binary unchanged.`

// pinentryWorkspacePrefix names the directory holding one prompt's handshake
// FIFOs, and its debug log when the run has one.
const pinentryWorkspacePrefix = "run-in-popup-pinentry-"

const pinentryExample = `  run-in-popup pinentry
  run-in-popup pinentry --backend zellij
  run-in-popup pinentry --backend tmux-floating-pane
  run-in-popup pinentry --pinentry /usr/bin/pinentry-tty -- --display :0`

func pinentryCmd(parent *cobra.Command, flagConfig *string) {
	var (
		flagBackend  string
		flagPinentry string
	)

	cmd := &cobra.Command{
		Use:     "pinentry [-- pinentry-arg...]",
		Short:   "Proxy a pinentry prompt into a popup",
		Long:    pinentryLong,
		Example: pinentryExample,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPinentry(cmd, args, *flagConfig, flagBackend, flagPinentry)
		},
	}

	cmd.Flags().StringVar(
		&flagBackend,
		"backend",
		"",
		fmt.Sprintf("popup backend, %s (default: auto-detected)", cli.BackendNameList()),
	)
	cmd.Flags().StringVar(
		&flagPinentry,
		"pinentry",
		"",
		"pinentry binary run on the popup tty (default: the configured pinentry_path)",
	)

	// gpg-agent owns this command's stdout for the Assuan exchange, and cobra
	// renders help to OutOrStdout, so help lands on stderr instead. Redirected
	// per render rather than by cmd.SetOut: the leaf's out writer is also what
	// __complete writes to, and completions must stay on stdout.
	renderHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		c.SetOut(c.ErrOrStderr())
		renderHelp(c, args)
	})

	parent.AddCommand(cmd)
}

func runPinentry(
	cmd *cobra.Command,
	args []string,
	flagConfig, flagBackend, flagPinentry string,
) (err error) {
	ctx := cmd.Context()

	cfg, err := runinpopup.LoadConfig(flagConfig)
	if err != nil {
		return err
	}

	rt, err := resolveRuntime(runtimeInputs{
		Config:    cfg,
		Overrides: pinentryFlagOverrides(cmd, flagBackend, flagPinentry),
	}, os.Environ())
	if err != nil {
		return err
	}

	workspace, err := runworkspace.Open(
		pinentryWorkspacePrefix,
		rt.UserData.Debug(),
		contextkey.ValueSlogLoggerFallback(ctx, slog.Default()),
	)
	if err != nil {
		return err
	}
	defer func() {
		// A debug log that would not close is worth reporting, but never worth
		// hiding how the exchange itself went.
		if cerr := workspace.Close(); err == nil {
			err = cerr
		}
	}()
	workspace.Logger.Info("PINENTRY_USER_DATA", slog.Any("data", rt.UserData))

	pinentry := &runinpopup.PinentryLauncher{
		Popup: &runinpopup.PopupLauncher{
			Backend:   rt.Backend,
			Logger:    workspace.Logger,
			Workspace: workspace.Options,
		},
		PinentryPath: rt.Config.PinentryPath,
		PinentryArgs: args,
		Timeouts:     rt.Config.Timeouts,
	}
	return pinentry.Call(ctx)
}

// pinentryFlagOverrides turns explicitly-set flags into the topmost config
// layer: a flag left alone stays absent from the partial, so the file and
// environment layers keep their say.
func pinentryFlagOverrides(cmd *cobra.Command, backend, pinentry string) runinpopup.PartialConfig {
	var p runinpopup.PartialConfig
	if cmd.Flags().Changed("backend") {
		p.DefaultBackend = &backend
	}
	if cmd.Flags().Changed("pinentry") {
		p.PinentryPath = &pinentry
	}
	return p
}
