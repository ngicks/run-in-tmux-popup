// Package shellargv renders argv as POSIX shell words, for multiplexer clients
// whose popup mechanism takes a command line rather than an argv.
package shellargv

import "strings"

// Quote renders s as a single POSIX shell word.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join renders an argv as a shell command line that executes it unchanged.
func Join(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	return strings.Join(quoted, " ")
}
