package commands

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/run-in-tmux-popup/runinpopup"
)

const execPayloadLong = `exec-payload is the popup half of "run-in-popup exec"
and is not meant to be typed: exec re-invokes this binary with it inside the
popup it opened, hands it the path of the result FIFO it is waiting on, and
reads back the JSON this writes there.`

func execPayloadCmd(parent *cobra.Command) {
	var flagStartupTimeout time.Duration

	cmd := &cobra.Command{
		Use:    runinpopup.ExecPayloadCommandName + " <result-fifo> -- command [arg...]",
		Short:  "Run the command exec asked for and report it back (internal)",
		Long:   execPayloadLong,
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExecPayload(cmd, args, flagStartupTimeout)
		},
	}

	cmd.Flags().DurationVar(
		&flagStartupTimeout,
		runinpopup.ExecPayloadStartupTimeoutFlag,
		0,
		"how long to wait for the caller's end of the result fifo"+
			" (default: the same bound the caller uses)",
	)

	parent.AddCommand(cmd)
}

func runExecPayload(cmd *cobra.Command, args []string, startupTimeout time.Duration) error {
	fifo, command := args[0], args[1:]
	if fifo == "" {
		return errors.New("the result fifo path must not be empty")
	}

	outcome, err := runinpopup.ExecPayload(
		cmd.Context(),
		fifo,
		command,
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
