package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/cli"
)

// configLongFmt documents the resolved-config shape so users can write --format
// templates without reading the source. The %s is filled with the shared
// template-helper docs (cli.TemplateFuncHelp). Keep the field list in sync with
// Config — it is a site on the add-a-field checklist.
const configLongFmt = `config loads every layer (defaults < file < environment), applies any
explicitly-set flags on top, and prints the fully-resolved configuration. With
no flags it prints indented JSON; with --format it renders a Go text/template
against the config value instead.

The value passed to --format has this shape (Go field name, type, JSON key);
nesting is shown as a tree so deep configs stay readable:

  Config
  ├─ .PinentryPath    string       # pinentry binary     (pinentry_path)
  ├─ .DefaultBackend  string       # backend to use      (default_backend)
  └─ .Timeouts                     # handshake timeouts  (timeouts)
      ├─ .Overall    time.Duration #   whole exchange    (timeouts.overall)
      ├─ .TTYRead    time.Duration #   read popup tty    (timeouts.tty_read)
      └─ .DoneWrite  time.Duration #   signal popup done (timeouts.done_write)

DefaultBackend is "tmux-popup", "zellij", or empty to auto-detect from the
environment. Durations print as nanosecond counts in JSON; the environment
layer accepts Go duration strings (RUN_IN_POPUP_TIMEOUTS_OVERALL=2m).

Use the Go field names in --format (e.g. {{.PinentryPath}}, or
{{.Timeouts.Overall}} for a nested field); the default JSON output uses the
lower-case keys shown in parentheses. The template also sees these helper
functions:

%s`

const configExample = `  run-in-popup config
  run-in-popup config --format '{{.PinentryPath}}'
  run-in-popup config --format '{{ json .Timeouts }}'`

func configCmd(parent *cobra.Command, flagConfig *string) {
	var flagFormat string

	cmd := &cobra.Command{
		Use:               "config",
		Short:             "Print the resolved configuration",
		Long:              fmt.Sprintf(configLongFmt, cli.TemplateFuncHelp()),
		Example:           configExample,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfig(cmd, args, *flagConfig, flagFormat)
		},
	}

	cmd.Flags().StringVarP(
		&flagFormat,
		"format",
		"f",
		"",
		"Go text/template rendered against the resolved config instead of JSON",
	)

	parent.AddCommand(cmd)
}

func runConfig(cmd *cobra.Command, _ []string, flagConfig, flagFormat string) error {
	cfg, err := runinpopup.LoadConfig(flagConfig)
	if err != nil {
		return err
	}
	// Presentation (JSON / template rendering) lives in runinpopup/cli; ./cmd
	// only wires it to stdout. cmd.Println would route to stderr.
	return cli.RenderConfig(cmd.OutOrStdout(), cfg, flagFormat)
}
