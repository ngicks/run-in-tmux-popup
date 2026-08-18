package commands

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

const execPayloadLong = `exec-payload is the popup half of "run-in-popup exec"
and is not meant to be typed: exec re-invokes this binary with it inside the
popup it opened, having wired its stdout to a FIFO of its own, and reads back
the JSON this writes there.`

func execPayloadCmd(parent *cobra.Command) {
	var flagStartupTimeout time.Duration

	cmd := &cobra.Command{
		Use:    runinpopup.ExecPayloadCommandName + " -- command [arg...]",
		Short:  "Run the command exec asked for and report it back (internal)",
		Long:   execPayloadLong,
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecPayload(cmd, args, flagStartupTimeout)
		},
	}

	cmd.Flags().DurationVar(
		&flagStartupTimeout,
		runinpopup.ExecPayloadStartupTimeoutFlag,
		0,
		"how long to give the caller to take the result"+
			" (default: the same bound the caller uses)",
	)

	parent.AddCommand(cmd)
}

func runExecPayload(cmd *cobra.Command, args []string, startupTimeout time.Duration) error {
	outcome, err := runinpopup.ExecPayload(
		cmd.Context(),
		args,
		runinpopup.ExecPayloadOptions{StartupTimeout: startupTimeout},
	)
	if !outcome.Ran {
		// Nothing was run, so there is no status to stand in for: report it the
		// ordinary way and let the entry point say so and exit 1.
		return err
	}
	// The multiplexer sees this process as the command the user asked for, so it
	// has to carry that command's status. err rides along: nil leaves a bare
	// status that prints nothing, and a failed report is printed on the popup
	// terminal because the caller was left without a result.
	return &exitCodeError{code: outcome.Status, err: err}
}
