package commands

import (
	"cmp"
	"log/slog"
	"os"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/internal/runworkspace"
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

const pinentryLong = `pinentry proxies the Assuan exchange gpg-agent runs over stdin/stdout to a
pinentry process drawing in a terminal-multiplexer popup: it opens the popup,
learns the tty it runs on and rewrites the "OPTION ttyname=" line so the prompt
appears there instead of on whichever terminal gpg-agent picked.

It is meant to be invoked by gpg-agent through a wrapper script that exports
PINENTRY_USER_DATA, whose fields locate the multiplexer:

  KIND:multiplexer_path:session_id:client_id:session_meta

KIND is "TMUX_POPUP" or "ZELLIJ_POPUP"; a "_DEBUG" suffix additionally writes a
debug log to log.txt in the temporary directory and keeps that directory around.

--backend wins over the configured default_backend, which in turn wins over
auto-detection from PINENTRY_USER_DATA, then $TMUX, then $ZELLIJ. Arguments
after "--" are passed to the pinentry binary unchanged.`

const pinentryExample = `  run-in-popup pinentry
  run-in-popup pinentry --backend zellij
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
		`popup backend, "tmux-popup" or "zellij" (default: auto-detected)`,
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
) error {
	ctx := cmd.Context()

	cfg, err := runinpopup.LoadConfig(flagConfig)
	if err != nil {
		return err
	}
	cfg = pinentryFlagOverrides(cmd, flagBackend, flagPinentry).Apply(cfg)

	userData := runinpopup.ParsePinentryUserData(os.Getenv("PINENTRY_USER_DATA"))

	backendName := cfg.DefaultBackend
	if backendName == "" {
		backendName, err = runinpopup.DetectBackendName(
			userData.Kind,
			os.Getenv("TMUX"),
			os.Getenv("ZELLIJ"),
		)
		if err != nil {
			return err
		}
	}
	backend, err := runinpopup.NewBackend(backendName, runinpopup.BackendOptions{
		BinaryPath:  userData.Path,
		SessionId:   userData.SessionId,
		ClientId:    userData.ClientId,
		SessionMeta: userData.SessionMeta,
		TMUX:        os.Getenv("TMUX"),
		// $SHELL rather than the library's "sh": the popup payload is the user's
		// login shell in every released version of this tool.
		Shell: cmp.Or(os.Getenv("SHELL"), "bash"),
	})
	if err != nil {
		return err
	}

	workspace, err := runworkspace.Open(
		"run-in-popup-pinentry-*",
		userData.Debug(),
		contextkey.ValueSlogLoggerFallback(ctx, slog.Default()),
	)
	if err != nil {
		return err
	}
	defer workspace.Close()
	workspace.Logger.Info("PINENTRY_USER_DATA", slog.Any("data", userData))

	return runinpopup.CallPinentry(ctx, backend, runinpopup.PinentryOptions{
		Logger:       workspace.Logger,
		TempDir:      workspace.Dir,
		PinentryPath: cfg.PinentryPath,
		PinentryArgs: args,
		Timeouts:     cfg.Timeouts,
	})
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
