# run-in-tmux-popup

Wrappers to call things in tmux popup

## {tmux,zellij}-popup-pinentry-curses

launches pinentry-curses in tmux popup.

build and place executables to somewhere you can look up through `$PATH`.

```
$ go build ./cmd/tmux-popup-pinentry-curses
$ go build ./cmd/zellij-popup-pinentry-curses
# copy to somewhere included in $PATH
$ mv *-popup-pinentry-curses ~/.local/bin
```

Set `$PINENTRY_USER_DATA` in somewhere your shell can load at startup as follow:

```bash
if [ -n "${TMUX}" ]; then
  export PINENTRY_USER_DATA="TMUX_POPUP:$(which tmux):${TMUX}"
elif [ -n "${ZELLIJ}" ]; then
  export PINENTRY_USER_DATA="ZELLIJ_POPUP:$(which zellij):${ZELLIJ_SESSION_NAME}"
fi
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
  exec $HOME/.local/bin/tmux-popup-pinentry-curses "$@"
  ;;
*ZELLIJ_POPUP*)
  exec $HOME/.local/bin/zellij-popup-pinentry-curses "$@"
esac

exec pinentry-qt "$@"
```

Then modify `~/.gnupg/gpg-agent.conf` to use the script file:

```conf
pinentry-program /home/ngicks/.local/scripts/pinentry.sh
```

### But why?

- Sometimes I ssh into my remote machine from somewhere no GUI is supported
- Calling pinentry-curses from lazygit called from neovim breakes terminal state.

calling pinentry-curses from tmux popup prevents this breakage.

Happy vibe coding!
