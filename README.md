# run-in-tmux-popup

Wrappers to call things in a terminal-multiplexer popup.

The current entrypoint is **`run-in-popup`**. Its `pinentry` subcommand proxies
the Assuan exchange gpg-agent runs over stdin/stdout to a `pinentry-curses`
drawing in a tmux `display-popup` or a zellij floating pane.

The older `tmux-popup-pinentry-curses` / `zellij-popup-pinentry-curses`
binaries still work but are [deprecated](#deprecated-legacy-binaries).

## Install

```
$ go install github.com/ngicks/run-in-tmux-popup/cmd/run-in-popup@latest
```

The module path still says `run-in-tmux-popup` while the binary is
`run-in-popup` — that is not a typo. The repository kept its name when zellij
support was added; only the command was renamed.

Or build it and drop it somewhere on `$PATH`:

```
$ go build ./cmd/run-in-popup
$ mv run-in-popup ~/.local/bin
```

## `run-in-popup pinentry`

```
Usage:
  run-in-popup pinentry [-- pinentry-arg...] [flags]

Flags:
      --backend string    popup backend, "tmux-popup" or "zellij" (default: auto-detected)
      --pinentry string   pinentry binary run on the popup tty (default: the configured pinentry_path)
```

It opens a popup whose only job is to report the tty it runs on, then runs
pinentry **outside** the popup on that tty, rewriting the `OPTION ttyname=`
line gpg-agent sends so the prompt appears in the popup instead of on whichever
terminal gpg-agent picked. The popup is dismissed once pinentry exits.

### 1. Export `$PINENTRY_USER_DATA`

gpg-agent forwards this variable verbatim to pinentry, so it is how the popup
learns which multiplexer to talk to. Set it somewhere your shell loads at
startup:

```bash
if [ -n "${TMUX}" ]; then
  export PINENTRY_USER_DATA="TMUX_POPUP:$(which tmux):$(tmux display -p '#{session_name}'):$(tmux display -p '#{client_tty}'):${TMUX}"
elif [ -n "${ZELLIJ}" ]; then
  export PINENTRY_USER_DATA="ZELLIJ_POPUP:$(which zellij):${ZELLIJ_SESSION_NAME}:"
fi
```

The format is colon-separated and positional:

```
(KIND):(path/to/bin):(session_id):(client_id):(session_meta)[:rest...]
```

| field          | meaning                                                                  |
| -------------- | ------------------------------------------------------------------------ |
| `KIND`         | `TMUX_POPUP` or `ZELLIJ_POPUP`, optionally with a `_DEBUG` suffix         |
| `path/to/bin`  | the multiplexer binary to invoke                                          |
| `session_id`   | the session hosting the popup — used by the zellij backend (`--session`)  |
| `client_id`    | the client to display the popup on — tmux only                            |
| `session_meta` | the `$TMUX` value, `socket_path,server_pid,session_index` — tmux only     |

Parsing tolerates a short value — trailing fields simply come out empty, and
anything after `session_meta` is kept as `rest` and otherwise ignored — but the
`tmux-popup` backend then rejects a missing `session_meta`:

```
tmux session meta is malformed: it must be something like "/run/user/1000/tmux-1000/default,111,0" but is ""
```

It is only skippable when the process already has `$TMUX` of its own, and
gpg-agent's children do not: the agent is a daemon started outside tmux. So for
tmux, keep the `${TMUX}` at the end. The `zellij` backend needs `session_id`
and ignores both tmux-only fields.

Unfortunately current `zellij` has no means to specify a client id to which the
display should be popped up, hence the empty `client_id` above.

A `_DEBUG` suffix on `KIND` (`TMUX_POPUP_DEBUG`) writes a debug log to
`log.txt` inside the run's temporary directory and keeps that directory around
instead of removing it.

### 2. Point gpg-agent at it

gpg-agent invokes a single pinentry program, so the usual setup is a wrapper
script that dispatches on `$PINENTRY_USER_DATA`:

```bash
#!/bin/bash

set -Ceu

case "${PINENTRY_USER_DATA-}" in
*TTY*)
  exec pinentry-curses "$@"
  ;;
*TMUX_POPUP* | *ZELLIJ_POPUP*)
  exec "$HOME/.local/bin/run-in-popup" pinentry -- "$@"
  ;;
esac

exec pinentry-qt "$@"
```

One branch covers both multiplexers: with no `--backend`, the backend is
auto-detected from `KIND`.

> [!IMPORTANT]
> Note the `--` before `"$@"`. `run-in-popup` parses its own flags, so pinentry
> arguments must be separated from them; without `--`, a pinentry flag is
> rejected:
>
> ```
> $ run-in-popup pinentry --display :0
> error: unknown flag: --display
> ```
>
> The legacy binaries pass `argv` through verbatim and need no separator, so
> this is the one edit an existing wrapper script needs.

Nothing but the Assuan protocol is ever written to stdout — logs, the `--log`
output, error messages and even `run-in-popup pinentry --help` all go to stderr
— so the command is safe to put behind `pinentry-program`. This subcommand is
the exception: every other command, `run-in-popup --help` and `config`
included, keeps printing to stdout.

Then point `~/.gnupg/gpg-agent.conf` at the script:

```conf
pinentry-program /home/ngicks/.local/scripts/pinentry.sh
```

### Backend selection

Valid backends are `tmux-popup` (tmux `display-popup`) and `zellij` (zellij
floating pane). They are named after the popup *mechanism*, not the
multiplexer, leaving room for a `tmux-floating-pane` backend later.

The backend is resolved in this order, first hit wins:

1. `--backend`
2. `default_backend` from the environment (`RUN_IN_POPUP_DEFAULT_BACKEND`) or
   the config file
3. auto-detection: `$PINENTRY_USER_DATA`'s `KIND`, then `$TMUX`, then
   `$ZELLIJ`

If nothing matches, the command fails and lists the valid values rather than
guessing.

### Configuration

`run-in-popup config` prints the fully-resolved configuration:

```
$ run-in-popup config
{
  "pinentry_path": "/usr/bin/pinentry-curses",
  "default_backend": "",
  "timeouts": {
    "overall": 120000000000,
    "tty_read": 20000000000,
    "done_write": 1000000000
  }
}
```

| key                   | meaning                                             | default                    |
| --------------------- | --------------------------------------------------- | -------------------------- |
| `pinentry_path`       | pinentry binary run on the popup tty                | `/usr/bin/pinentry-curses` |
| `default_backend`     | backend to use; empty means auto-detect             | `""`                       |
| `timeouts.overall`    | bounds the whole popup/pinentry exchange            | 2m                         |
| `timeouts.tty_read`   | bounds reading the popup's tty from the FIFO        | 20s                        |
| `timeouts.done_write` | bounds signalling the popup to close                | 1s                         |

Layers apply lowest to highest: **defaults < file < environment < flags**. A
layer only overrides the keys it actually sets.

The config file is **JSON**, read from the first of:

1. `--config <path>`
2. `$RUN_IN_POPUP_CONF`
3. `~/.config/run-in-popup/config.json` — Go's `os.UserConfigDir()`, so
   `$XDG_CONFIG_HOME` is honored when set

A missing file is not an error. Only the keys you want to change need to be
present:

```json
{
  "pinentry_path": "/usr/bin/pinentry-tty",
  "timeouts": { "overall": 60000000000 }
}
```

Every key also has an environment variable, prefixed `RUN_IN_POPUP_`:
`RUN_IN_POPUP_PINENTRY_PATH`, `RUN_IN_POPUP_DEFAULT_BACKEND`,
`RUN_IN_POPUP_TIMEOUTS_OVERALL`, `RUN_IN_POPUP_TIMEOUTS_TTY_READ`,
`RUN_IN_POPUP_TIMEOUTS_DONE_WRITE`. Durations are nanosecond counts in JSON but
accept Go duration strings in the environment
(`RUN_IN_POPUP_TIMEOUTS_OVERALL=2m`).

## Deprecated: legacy binaries

`tmux-popup-pinentry-curses` and `zellij-popup-pinentry-curses` are
**deprecated**. They still build, still take the same `$PINENTRY_USER_DATA`
contract and still pass `argv` straight through to pinentry — existing setups
keep working — but they are now thin shims over the same code
`run-in-popup pinentry` runs, and each prints a deprecation notice at startup:

```
tmux-popup-pinentry-curses is deprecated; run "run-in-popup pinentry --backend tmux-popup" instead.
```

The notice goes to **stderr only**. The Assuan protocol owns stdin/stdout, so
nothing but the protocol may be written there.

| deprecated binary               | replacement                                     |
| ------------------------------- | ----------------------------------------------- |
| `tmux-popup-pinentry-curses`    | `run-in-popup pinentry --backend tmux-popup`    |
| `zellij-popup-pinentry-curses`  | `run-in-popup pinentry --backend zellij`        |

`--backend` is optional in both cases when `$PINENTRY_USER_DATA` is set, since
its `KIND` selects the same backend.

To migrate a wrapper script, replace the two branches and **add `--` before the
passed-through arguments** (see the note [above](#2-point-gpg-agent-at-it)):

```diff
 *TMUX_POPUP*)
-  exec $HOME/.local/bin/tmux-popup-pinentry-curses "$@"
+  exec "$HOME/.local/bin/run-in-popup" pinentry --backend tmux-popup -- "$@"
   ;;
 *ZELLIJ_POPUP*)
-  exec $HOME/.local/bin/zellij-popup-pinentry-curses "$@"
+  exec "$HOME/.local/bin/run-in-popup" pinentry --backend zellij -- "$@"
```

A few other differences worth knowing:

- The shims read no configuration — no config file, no `RUN_IN_POPUP_*`
  variables — so they always run `/usr/bin/pinentry-curses` with the built-in
  timeouts. Only `run-in-popup pinentry` is tunable.
- The shims additionally accept `TMUX_POPUP_DEBUG=1` as a debug switch;
  `run-in-popup pinentry` honors only the `_DEBUG` suffix on
  `$PINENTRY_USER_DATA`'s `KIND`.
- Both shims reject an empty `session_id` up front, even the tmux one, which
  does not otherwise use the field. `run-in-popup pinentry` only requires what
  the selected backend actually reads.

## Library

The logic lives in `runinpopup` and is importable:

```go
import "github.com/ngicks/run-in-tmux-popup/runinpopup"
```

`Run` opens a popup and executes an arbitrary command in it; `CallPinentry` is
the pinentry proxy layered on the same mechanism. Backends are constructed
explicitly (`NewTmuxPopupBackend`, `NewZellijBackend`, or `NewBackend` by
name) from values the caller supplies — the package never reads the
environment on its own.

### But why?

- Sometimes I ssh into my remote machine from somewhere no GUI is supported
- Calling pinentry-curses from lazygit called from neovim breakes terminal state.

calling pinentry-curses from tmux popup prevents this breakage.

Happy vibe coding!
