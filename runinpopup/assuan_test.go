package runinpopup

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// The transcripts are pinned byte for byte: what this produces is the exact
// stream a pinentry child reads, and gpg-agent's half of the protocol is not
// ours to reinterpret.
func TestRewriteAssuanTTY(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "the announced terminal replaces the one gpg-agent picked",
			in:   "OPTION lc-ctype=en_US.UTF-8\nOPTION ttyname=/dev/pts/9\nGETPIN\n",
			want: "OPTION lc-ctype=en_US.UTF-8\nOPTION ttyname=" + popupTTY + "\nGETPIN\n",
		},
		{
			name: "an option with no value is still the option",
			in:   "OPTION ttyname=\n",
			want: "OPTION ttyname=" + popupTTY + "\n",
		},
		{
			name: "everything the option is not passes through",
			in: "OPTION ttyname\n OPTION ttyname=/dev/pts/9\noption ttyname=/dev/pts/9\n" +
				"OPTION ttynameX=/dev/pts/9\nSETPROMPT PIN:\n\nD  padded  data \nBYE\n",
			want: "OPTION ttyname\n OPTION ttyname=/dev/pts/9\noption ttyname=/dev/pts/9\n" +
				"OPTION ttynameX=/dev/pts/9\nSETPROMPT PIN:\n\nD  padded  data \nBYE\n",
		},
		{
			name: "a crlf line ending is normalized",
			in:   "OPTION grab\r\nBYE\r\n",
			want: "OPTION grab\nBYE\n",
		},
		{
			name: "a stream ending without a newline is terminated anyway",
			in:   "BYE",
			want: "BYE\n",
		},
		{
			name: "an empty stream forwards nothing",
			in:   "",
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := rewriteAssuanTTY(strings.NewReader(tc.in), &out, popupTTY); err != nil {
				t.Fatalf("rewriteAssuanTTY: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("forwarded:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// A line longer than the scanner's buffer is not a line pinentry could act on,
// and guessing where to split it would invent a command nobody sent: the relay
// ends instead, with what came before it already delivered.
func TestRewriteAssuanTTY_overlongLine(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader(
		"GETPIN\n" + strings.Repeat("D", bufio.MaxScanTokenSize+1) + "\nBYE\n",
	)

	err := rewriteAssuanTTY(in, &out, popupTTY)

	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("err = %v, want the overlong line reported", err)
	}
	if got := out.String(); got != "GETPIN\n" {
		t.Errorf("forwarded %q, want everything before the overlong line", got)
	}
}

func TestRewriteAssuanTTY_readFailure(t *testing.T) {
	readErr := errors.New("the assuan input went away")
	var out bytes.Buffer

	err := rewriteAssuanTTY(
		io.MultiReader(strings.NewReader("GETPIN\n"), errReader{readErr}),
		&out,
		popupTTY,
	)

	if !errors.Is(err, readErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, readErr)
	}
	if got := out.String(); got != "GETPIN\n" {
		t.Errorf("forwarded %q, want what arrived before the failure", got)
	}
}

func TestRewriteAssuanTTY_writeFailure(t *testing.T) {
	writeErr := errors.New("pinentry is gone")

	err := rewriteAssuanTTY(
		strings.NewReader("OPTION ttyname=/dev/pts/9\nBYE\n"),
		errWriter{writeErr},
		popupTTY,
	)

	if !errors.Is(err, writeErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, writeErr)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

type errWriter struct{ err error }

func (w errWriter) Write(p []byte) (int, error) { return 0, w.err }
