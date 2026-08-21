package runinpopup

import (
	"bufio"
	"io"
	"strings"
)

// assuanTTYOption is the line gpg-agent sends to name the terminal pinentry
// should draw on. Only this exact spelling is a match: Assuan commands are
// case-sensitive and unindented, so a line that merely looks like it is one of
// the payload's own and must reach pinentry untouched.
const assuanTTYOption = "OPTION ttyname="

// rewriteAssuanTTY copies the Assuan stream from r to w, replacing the terminal
// gpg-agent named with tty.
//
// gpg-agent names whichever terminal it happened to inherit; the prompt belongs
// in the popup instead, and this one line is the whole difference between the
// two. Everything else is forwarded as it came, except that line endings are
// normalized to "\n" — the scan strips a "\r" a sender may add, and pinentry
// wants the line without it.
//
// It returns nil when the stream ends, and otherwise the error that ended it:
// the reader's, the writer's, or [io.ErrClosedPipe] when the caller closed r to
// end the relay.
func rewriteAssuanTTY(r io.Reader, w io.Writer, tty string) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, assuanTTYOption) {
			line = assuanTTYOption + tty
		}
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}
