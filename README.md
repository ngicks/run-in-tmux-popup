# run-in-tmux-popup

Wrappers to call things in a terminal-multiplexer popup.

The current entrypoint is **`run-in-popup`**. Its `pinentry` subcommand proxies
the Assuan exchange gpg-agent runs over stdin/stdout to a `pinentry-curses`
drawing in a tmux `display-popup`, a tmux floating pane or a zellij floating
pane. Its [`exec`](#run-in-popup-exec) subcommand runs any command in such a
popup and hands the result back to the caller as JSON.

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
      --backend string    popup backend, "tmux-popup", "tmux-floating-pane" or "zellij" (default: auto-detected)
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

To open a tmux **floating pane** instead of a `display-popup`, use
`TMUX_FLOATING_PANE` as the `KIND`. It ignores `client_id`, but the field is
positional, so its colon has to stay:

```bash
export PINENTRY_USER_DATA="TMUX_FLOATING_PANE:$(which tmux):$(tmux display -p '#{session_name}')::${TMUX}"
```

The format is colon-separated and positional:

```
(KIND):(path/to/bin):(session_id):(client_id):(session_meta)[:rest...]
```

| field          | meaning                                                                  |
| -------------- | ------------------------------------------------------------------------ |
| `KIND`         | `TMUX_POPUP`, `TMUX_FLOATING_PANE` or `ZELLIJ_POPUP`, optionally with a `_DEBUG` suffix |
| `path/to/bin`  | the multiplexer binary to invoke                                          |
| `session_id`   | the session hosting the popup — used by `zellij` (`--session`) and `tmux-floating-pane` (`-t`) |
| `client_id`    | the client to display the popup on — `tmux-popup` only                    |
| `session_meta` | the `$TMUX` value, `socket_path,server_pid,session_index` — tmux only     |

Parsing tolerates a short value — trailing fields simply come out empty, and
anything after `session_meta` is kept as `rest` and otherwise ignored — but both
tmux backends then reject a missing `session_meta`:

```
tmux session meta is malformed: it must be something like "/run/user/1000/tmux-1000/default,111,0" but is ""
```

It is only skippable when the process already has `$TMUX` of its own, and
gpg-agent's children do not: the agent is a daemon started outside tmux. So for
tmux, keep the `${TMUX}` at the end. The `zellij` backend needs `session_id`
and ignores both tmux-only fields.

Unfortunately current `zellij` has no means to specify a client id to which the
display should be popped up, hence the empty `client_id` above. `client_id` is
just as useless to `tmux-floating-pane`: `new-pane` has no client-targeting flag
because the pane lives in a window, and every client viewing that window sees
it. That backend uses `session_id` instead, so fill it in as the snippet above
already does.

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
*TMUX_POPUP* | *TMUX_FLOATING_PANE* | *ZELLIJ_POPUP*)
  exec "$HOME/.local/bin/run-in-popup" pinentry -- "$@"
  ;;
esac

exec pinentry-qt "$@"
```

One branch covers every backend: with no `--backend`, the backend is
auto-detected from `KIND`. A `KIND` the script does not match falls through to
the `pinentry-qt` line, so keep the patterns in sync with the `KIND` you export.

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

Backends are named after the popup *mechanism*, not the multiplexer, because
tmux has two of them:

| backend              | mechanism                             | targets      |
| -------------------- | ------------------------------------- | ------------ |
| `tmux-popup`         | `tmux display-popup -E`               | `client_id`  |
| `tmux-floating-pane` | `tmux new-pane` (the `*` binding)     | `session_id` |
| `zellij`             | `zellij run --floating`               | `session_id` |

`tmux-floating-pane` needs a tmux with the `new-pane` command — bound to `*` by
default, and verified here against tmux 3.7b. Unlike a `display-popup`, the pane
it opens is a real pane: it is part of the window, so every client viewing that
window sees it, and there is no client targeting.

The backend is resolved in this order, first hit wins:

1. `--backend`
2. `default_backend` from the environment (`RUN_IN_POPUP_DEFAULT_BACKEND`) or
   the config file
3. auto-detection: `$PINENTRY_USER_DATA`'s `KIND`, then `$TMUX`, then
   `$ZELLIJ`

If nothing matches, the command fails and lists the valid values rather than
guessing.

Auto-detection only picks `tmux-floating-pane` from an explicit
`TMUX_FLOATING_PANE` `KIND`. A bare `$TMUX` names the multiplexer, not one of
its two mechanisms, and keeps resolving to `tmux-popup`.

> [!WARNING]
> **tmux 3.7b crashes the whole server** when a floating pane is created while a
> pane in the window is zoomed — the server dies as the floating pane exits,
> taking every session with it. `display-popup` is unaffected, so this only
> concerns `tmux-floating-pane`. The fix is in tmux 3.7c.
>
> The backend works around it: before opening the pane it runs `tmux -V`, and on
> anything it cannot identify as 3.7c or later it checks `#{window_zoomed_flag}`
> and de-zooms first, re-zooming the same pane once the popup is gone. A version
> string it cannot parse — including the `next-3.8` of development builds, which
> pins no commit — counts as affected, since a needless de-zoom is a flicker and
> a missed one is a dead server. Re-zooming is best-effort: if it fails the
> window is left unzoomed and the failure is logged, never fatal.

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
| `default_backend`     | backend to use (see [above](#backend-selection)); empty means auto-detect | `""`  |
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

## `run-in-popup exec`

```
Usage:
  run-in-popup exec [flags] -- command [arg...]

Flags:
      --backend string   popup backend, "tmux-popup", "tmux-floating-pane" or "zellij" (default: auto-detected)
      --title string     popup title (default: the backend's own; tmux-floating-pane has no title flag and ignores it)
```

It opens a popup, runs the command in it — drawn there live, with the popup's
terminal as its stdin, so it may prompt — and once the command exits prints a
single compact JSON object on **its own** stdout, back in the shell that called
it:

```
$ run-in-popup exec -- sh -c 'echo built; exit 2'
{"command":["sh","-c","echo built; exit 2"],"exit_code":2,"stdout":"built\n","stderr":""}
```

| key                  | meaning                                                                        |
| -------------------- | ------------------------------------------------------------------------------ |
| `command`            | the argv that was run                                                          |
| `exit_code`          | the command's status, `-1` when it never started or was killed by a signal     |
| `stdout` / `stderr`  | everything the command wrote, captured whole                                   |
| `error`              | present only when there is no status to report — see below                     |

`error` appears when the command never started, or when its output could not be
relayed; `exit_code` is then `-1` rather than anything the command chose.

`run-in-popup exec` itself **exits 0 whenever the exchange worked**. A command
that fails is not a failure of the transport: its status is `exit_code`, and the
caller reads it out of the JSON.

```
$ run-in-popup exec -- make test | jq -r .exit_code
```

Everything after `--` is the command and is passed through untouched; without a
`--`, bare arguments work as long as the command carries no flags of its own.
The backend is chosen exactly as it is for `pinentry` — see
[Backend selection](#backend-selection).

A few things worth knowing:

- The command's stdout and stderr are teed — live to the popup, captured for the
  result — so they are pipes rather than the popup's tty. Programs that gate
  color or progress rendering on `isatty` therefore render plainly. stdin *is*
  the tty, so prompting still works.
- `timeouts.overall` does **not** apply here. It sizes a pinentry prompt, and any
  bound tight enough for that would kill the long builds this exists to run.
  Only the popup's *startup* is on a clock — 30 s for it to get as far as running
  the command — after which the command runs for as long as it likes, and only
  your own Ctrl-C ends the wait.
- A popup that dies mid-command is reported, not waited on: `exec` fails with
  `the popup payload exited without reporting a result` rather than hanging.
- `--title` is dropped by `tmux-floating-pane`: `new-pane` has no title flag. It
  reaches `tmux-popup` (as `-T`) and `zellij` (as `--name`).

The popup runs this same binary again, as a hidden `exec-payload` subcommand
whose stdout is the FIFO the result travels back through. It is an
implementation detail — never invoke it by hand.

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
the pinentry proxy layered on the same mechanism, and `CallExec` is the exec
round trip, which pairs with `ExecPayload` on the popup side. Backends are
constructed explicitly (`NewTmuxPopupBackend`, `NewTmuxFloatingPaneBackend`,
`NewZellijBackend`, or `NewBackend` by name) from values the caller supplies —
the package never reads the environment on its own.

`Backend.Prepare` is where a backend fixes up multiplexer state a popup would
otherwise break — the tmux de-zoom above is its one implementation — and returns
a restore func every entry point calls on the way out.

None of them waits for the popup to be gone first. `Run` returns as soon as
`new-pane` does, while the pane it created is still alive; `CallPinentry`
returns once it has written the done FIFO, without waiting for the pane to act
on it; `CallExec` returns once the result is on the FIFO, which the payload
writes just before it exits. With `tmux-floating-pane` the re-zoom can therefore
land on a live floating pane, which tmux answers by pulling that pane out of its
float and into the layout — not by crashing. That is the guarantee this relies
on; it is not an ordering guarantee.

### But why?

- Sometimes I ssh into my remote machine from somewhere no GUI is supported
- Calling pinentry-curses from lazygit called from neovim breakes terminal state.

calling pinentry-curses from tmux popup prevents this breakage.

Happy vibe coding!
