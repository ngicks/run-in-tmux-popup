// Package commands contains the cobra commands for run-in-popup.
package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/internal/loggerfactory"
)

func Execute(ctx context.Context) error {
	return rootCmd().ExecuteContext(ctx)
}

func rootCmd() *cobra.Command {
	var (
		logConfig   *loggerfactory.Config
		flagVersion bool
		flagConfig  string
	)

	cmd := &cobra.Command{
		Use:           "run-in-popup",
		Short:         "Run a command in a terminal multiplexer popup.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if err := loggerfactory.ReadEnv(logConfig, "run-in-popup", os.Environ()); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
			logger := loggerfactory.BuildLogger(logConfig)
			slog.SetDefault(logger)
			cmd.SetContext(contextkey.WithSlogLogger(cmd.Context(), logger))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagVersion {
				return runVersion(cmd, args)
			}
			return runRoot(cmd, args)
		},
	}

	logConfig = loggerfactory.RegisterFlags(cmd)
	cmd.Flags().BoolVar(&flagVersion, "version", false, "alias for the version subcommand")
	cmd.PersistentFlags().
		StringVar(&flagConfig, "config", "", "config file path; overrides the default location")

	versionCmd(cmd)
	configCmd(cmd, &flagConfig)
	pinentryCmd(cmd, &flagConfig)

	// TODO: declare additional root flags inside the `var (...)` block above
	// and bind them with `cmd.PersistentFlags().<Type>Var(&flag, ...)`. Extend
	// the RunE closure to forward captured values into runRoot.

	// TODO: wire additional subcommands here, passing &flagConfig to the ones
	// that load config, e.g.:
	//   runCmd(cmd, &flagConfig)

	// TODO: you may add initialization logic for root internal service construct here.

	return cmd
}

func runRoot(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}
