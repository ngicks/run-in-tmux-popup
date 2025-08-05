# run-in-tmux-popup

Wrappers to call things in tmux popup

## tmux-popup-pinentry-curses

launches pinentry-curses in tmux popup.

build and place executables to somewhere you can look up through `$PATH`.

```
$ go build ./cmd/tmux-popup-pinentry-curses
# copy to somewhere included in $PATH
$ mv tmux-popup-pinentry-curses ~/.local/bin
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
esac

exec pinentry-qt "$@"
```

Then modify `~/.gnupg/gpg-agent.conf` to use the script file:

```conf
pinentry-program /home/ngicks/.local/scripts/pinentry.sh
```

You may also want to place script lines somewhere in your start up script.

```bash
if [ -t 0 ]; then
  # Set GPG_TTY so gpg-agent knows where to prompt.  See gpg-agent(1)
  export GPG_TTY="$(tty)"
fi

# but in tmux use tmux display-pop and pientry-curses
if [ -n "${TMUX}" ]; then
  export PINENTRY_USER_DATA="TMUX_POPUP"
fi
```

### But why?

- Sometimes I ssh into my remote machine from somewhere no GUI is supported
- Calling pinentry-curses from lazygit called from neovim breakes terminal state.

calling pinentry-curses from tmux popup prevents this breakage.

Happy vibe coding!
