package flag

import (
	"flag"
	"fmt"
	"os"
)

var (
	GlobalOption Option
	FlagSet      = flag.NewFlagSet("pickentry", flag.ExitOnError)
)

func Parse() {
	GlobalOption.Parse(FlagSet, os.Args[1:])
}

// Usage prints usage information to stderr.
func Usage() {
	fmt.Fprintf(os.Stderr, "Usage: pickentry [options]\n\n")
	fmt.Fprintf(os.Stderr, "Options:\n")
	fmt.Fprintf(os.Stderr, "  -f, --choices-file  Path to JSON file containing items\n")
	fmt.Fprintf(os.Stderr, "  -c, --choices       JSON array of items passed directly as string\n")
	fmt.Fprintf(os.Stderr, "  -p, --prompt        Custom text to display at the top\n")
	fmt.Fprintf(os.Stderr, "      --callback      Shell command template using Go text/template format\n")
	fmt.Fprintf(os.Stderr, "  -s, --shell         Shell to execute callback (default: $SHELL or /bin/sh)\n")
	fmt.Fprintf(os.Stderr, "      --tmux[=OPTS]   Run in tmux popup [center|top|bottom|left|right][,WIDTH[%%]][,HEIGHT[%%]]\n")
	fmt.Fprintf(os.Stderr, "      --zellij[=OPTS] Run in zellij floating pane (not yet implemented)\n")
	fmt.Fprintf(os.Stderr, "      --no-popup      Disable popup even if --tmux/--zellij is set\n")
	fmt.Fprintf(os.Stderr, "\nItem JSON format: [{\"id\": \"...\", \"cmd\": \"...\"}]\n")
	fmt.Fprintf(os.Stderr, "Callback template fields: {{.Id}}, {{.Cmd}}\n")
}

// Option holds the parsed command-line flags.
type Option struct {
	ChoicesFile string
	Choices     string
	Prompt      string
	Callback    string
	Shell       string
	Tmux        string
	Zellij      string
	NoPopup     bool
	ResultFifo  string
}

func (o *Option) Parse(fs *flag.FlagSet, args []string) {
	fs.Usage = Usage

	fs.StringVar(&o.ChoicesFile, "choices-file", "", "Path to JSON file containing items")
	fs.StringVar(&o.Choices, "choices", "", "JSON array of items passed directly as string")
	fs.StringVar(&o.Prompt, "prompt", "select input", "Custom text to display at the top")
	fs.StringVar(&o.Callback, "callback", "", "Shell command template using Go text/template format")
	fs.StringVar(&o.Shell, "shell", "", "Shell to execute callback")

	fs.BoolFunc("tmux", "Run in tmux popup [pos][,W%][,H%]", func(s string) error {
		if s == "true" {
			o.Tmux = "default"
		} else {
			o.Tmux = s
		}
		return nil
	})
	fs.BoolFunc("zellij", "Run in zellij floating pane [W%][,H%]", func(s string) error {
		if s == "true" {
			o.Zellij = "default"
		} else {
			o.Zellij = s
		}
		return nil
	})
	fs.BoolVar(&o.NoPopup, "no-popup", false, "Disable popup even if --tmux/--zellij is set")
	fs.StringVar(&o.ResultFifo, "result-fifo", "", "")

	var choicesFileShort, choicesShort, promptShort, shellShort string
	fs.StringVar(&choicesFileShort, "f", "", "Path to JSON file containing items (shorthand)")
	fs.StringVar(&choicesShort, "c", "", "JSON array of items passed directly as string (shorthand)")
	fs.StringVar(&promptShort, "p", "", "Custom text to display at the top (shorthand)")
	fs.StringVar(&shellShort, "s", "", "Shell to execute callback (shorthand)")

	fs.Parse(args)

	// Resolve shorthand flags
	if o.ChoicesFile == "" {
		o.ChoicesFile = choicesFileShort
	}
	if o.Choices == "" {
		o.Choices = choicesShort
	}
	if o.Prompt == "" {
		o.Prompt = promptShort
	}
	if o.Shell == "" {
		o.Shell = shellShort
	}
}
