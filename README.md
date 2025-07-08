# run-in-tmux-popup

Wrappers to call things in tmux popup

## tmux-popup-pinentry-launcher

launches pinentry program that takes control over a `tty`, e.g. `pinentry-curses` and `pinentry-tty`, in tmux popup.

build and place executables to somewhere you can look up through `$PATH`.

```go
$ go build ./cmd/tmux-popup-pinentry-launcher/
$ go build ./cmd/tmux-popup-pinentry-executor/
# copy to somewhere included in $PATH
$ mv tmux-popup-pinentry-* ~/.local/bin
```

then make a wrapper script for pinentry caller.

Something like below:

```bash
#!/bin/bash

set -Ceu

case "${PINENTRY_USER_DATA-}" in
*TTY*)
  exec pinentry-curses "$@"
  ;;
*TMUX_POPUP*)
  exec $HOME/.local/bin/tmux-popup-pinentry-launcher pinentry-curses "@"
  ;;
esac

exec pinentry-qt "$@"
```

Then modify `~/.gnupg/gpg-agent.conf` to use the script file:

```conf
pinentry-program /home/ngicks/.local/scripts/pinentry.sh
```

