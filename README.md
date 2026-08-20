# run-in-tmux-popup

Wrappers to call things in a terminal-multiplexer popup.

The current entrypoint is **`run-in-popup`**. Its `pinentry` subcommand proxies
the Assuan exchange gpg-agent runs over stdin/stdout to a `pinentry-curses`
drawing in a tmux `display-popup`, a tmux floating pane or a zellij floating
pane. Its [`exec`](#run-in-popup-exec) subcommand runs any command in such a
popup, feeds it whatever the calling shell pipes in, and relays what it writes
back to the terminal that called it.

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
2. `backend` from the environment (`RUN_IN_POPUP_BACKEND`) or the config file
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
  "backend": "",
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
| `backend`             | backend to use (see [above](#backend-selection)); empty means auto-detect | `""`  |
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
`RUN_IN_POPUP_PINENTRY_PATH`, `RUN_IN_POPUP_BACKEND`,
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
      --height string    popup height, same syntax as --width
  -h, --help             help for exec
      --title string     popup title (default: the backend's own; tmux-floating-pane has no title flag and ignores it)
  -w, --width string     popup width: cells or "N%" (default: the backend's own)
      --x string         popup x position: cells, "N%" or a tmux position specifier C/R/P/M/W/S, which zellij rejects (default: the backend's own)
      --y string         popup y position, same syntax as --x (tmux-popup needs --height in the same unit as a numeric --y)
```

It opens a popup and lets it run the command **on the popup's own terminal**. The
command's stdin, stdout and stderr are that pane, so an interactive program
simply works there and has to know nothing about being run this way:

```
$ run-in-popup exec -- htop
```

A bridge back to the calling terminal is there for a command that wants one, on
descriptors **beside** its stdio rather than in place of it. Whatever is piped
into `run-in-popup exec` is readable on **fd 3**, everything written to **fd 4**
is relayed to `exec`'s **own** stdout and everything written to **fd 5** to its
stderr — as it arrives, unaltered, and each stream on its own. `TTY_IN`,
`TTY_OUT` and `TTY_ERR` hold the three FIFO paths for a program that cannot
inherit descriptors and has to open them by name. A stream the caller does not
supply is allocated neither a descriptor nor a variable.

A pipeline through the popup therefore has to say so in the command. `fzf` reads
its candidate list from stdin and prints the selection to stdout, but falls back
to generating the list itself when stdin is a terminal — which in the popup it
is — so both ends need naming:

```
$ file=$(find . -type f | run-in-popup exec -- sh -c 'fzf <&3 >&4')
```

`fzf` keeps its interface off stdout, so moving only stdout leaves the list drawn
in the popup and puts the chosen line in the caller's shell. Use `<&3` rather
than `</dev/fd/3`: the former inherits the descriptor the popup was handed, while
the latter re-opens the FIFO by path and blocks forever if the caller's side has
already sent everything and closed.

`run-in-popup exec` **exits 0 once the bridge is over** — the popup opened and
both output streams ended — and **1** when the popup could not be opened, never
reached the command, or a stream could not be relayed. The command's own status
is not passed on: only some popup mechanisms carry it back at all, so reporting
it would mean a different answer per backend. A caller that needs it has to have
the command report it in what it writes:

```
$ run-in-popup exec -- sh -c 'make test; echo "exit=$?" >&4'
```

Everything after `--` is the command and is passed through untouched; without a
`--`, bare arguments work as long as the command carries no flags of its own.
The backend is chosen exactly as it is for `pinentry` — see
[Backend selection](#backend-selection).

`--x`, `--y`, `--width` (`-w`) and `--height` place and size the popup in the
vocabulary tmux takes: a bare number is terminal cells, `N%` a percentage of the
terminal, and `--x` / `--y` additionally accept tmux's position specifiers —
`C` the centre of the terminal, `R` its right side, `P` the bottom left of the
pane, `M` the mouse position, `W` the window position on the status line, `S`
the line above or below it. A flag left unset leaves the backend's own
placement.

`--x` and `--y` are the popup's **top-left corner**, the same on every backend.
That is not what every popup mechanism takes natively — tmux's `display-popup`
places a popup by its bottom edge — so the `tmux-popup` backend adds the height
to a numeric `--y` for you, and needs `--height` in the same unit to do it: a
numeric or percentage `--y` with no `--height`, or with one in the other unit,
fails the launch instead of guessing. The other backends take both coordinates
as written. A popup that would fall outside the terminal is still tmux's to
clamp.

```
$ run-in-popup exec --width 80% --height 20 -- htop
$ run-in-popup exec --x 0 --y 5 --height 20 -- htop   # top edge on row 5
```

Only the tmux backends understand the specifiers; `zellij` takes cells and
percentages and refuses a specifier by name rather than placing the pane
somewhere else. A malformed value fails before any popup is opened. `--height`
has no shorthand: `-h` is `--help`.

A few things worth knowing:

- The command's three standard streams are the popup's tty, so `isatty` holds
  there and colors, progress rendering and prompts behave as they would in any
  terminal. Nothing it prints on them reaches the caller: `exec`'s own stdout and
  stderr carry fd 4 and fd 5, and nothing else.
- `timeouts.overall` does **not** apply here. It sizes a pinentry prompt, and any
  bound tight enough for that would kill the long builds this exists to run.
  Only the popup's *startup* is on a clock — 30 s for it to get as far as running
  the command and opening its end of each stream — after which the command runs
  for as long as it likes, and only your own Ctrl-C ends the wait.
- A popup dismissed mid-command is not waited on: it takes the command with it,
  which ends both output streams, so `exec` returns with whatever had already
  arrived rather than hanging.
- The two output streams are what the wait is for. A caller's own stdin — a
  terminal nobody is typing at, say — never holds the bridge open past the
  command it was feeding.
- `--title` is dropped by `tmux-floating-pane`: `new-pane` has no title flag. It
  reaches `tmux-popup` (as `-T`) and `zellij` (as `--name`).

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
import (
	"github.com/ngicks/run-in-tmux-popup/runinpopup"
	"github.com/ngicks/run-in-tmux-popup/runinpopup/backend"
)
```

Backends are built from coordinates the caller supplies: `backend.New(name,
backend.Options)`, with `backend.Names()` listing the valid names and
`backend.DetectName(userDataKind, tmuxEnv, zellijEnv)` picking one from the same
hints the CLI uses. Detection is pure and so is construction: the caller reads
the environment and passes the hints in, and a backend holds only what it was
handed. (`LoadConfig` is the one exception, and reading `$RUN_IN_POPUP_*` is its
whole job.)

`PopupLauncher` is the launch layer. It holds the `Backend` and everything a
launch needs beyond the payload itself — `Logger`, `Workspace` for the directory
the payload's FIFOs live in, `StartupTimeout` for the rendezvous with the payload
(30 s when zero) — and `Exec(ctx, PopupSpec, PopupStreams)` opens one popup and
hands back a `*PopupCommand`:

```go
name, err := backend.DetectName("", os.Getenv("TMUX"), os.Getenv("ZELLIJ"))
if err != nil {
	return err
}
b, err := backend.New(name, backend.Options{
	TMUX:      os.Getenv("TMUX"),
	SessionId: os.Getenv("ZELLIJ_SESSION_NAME"),
})
if err != nil {
	return err
}

launcher := &runinpopup.PopupLauncher{Backend: b}
popup, err := launcher.Exec(
	ctx,
	runinpopup.PopupSpec{Title: "build", Command: []string{"go", "build", "./..."}},
	runinpopup.PopupStreams{}, // no stream named: stdio stays on the popup's terminal
)
if err != nil {
	return err
}
return popup.Wait()
```

`PopupSpec` also carries where the popup goes and how big it is — `X`, `Y`,
`Width` and `Height`, the values [`exec`'s flags](#run-in-popup-exec) take, in
the same syntax. `Exec` validates them before it opens, prepares or allocates
anything.

`PopupStreams` decides which of the payload's streams are allocated, under one
rule per stream: nil allocates nothing, and a non-nil endpoint gets a FIFO
relayed to it. `StdoutPipe`/`StderrPipe` ask for a reader instead, handed back by
`PopupCommand.StdoutPipe` / `StderrPipe` — os/exec style, so read them to EOF
before `Wait`.

`KeepStdio` decides where an allocated FIFO lands inside the popup, and nothing
else — the endpoints and the pipe requests behave the same either way. Off, each
FIFO takes over the stdio it stands for, which is what a payload speaking a
protocol over its stdout wants. On, the payload's fd 0, 1 and 2 stay on the
popup's terminal and the FIFOs arrive beside them on fd 3, fd 4 and fd 5, with
their paths in `TTY_IN`/`TTY_OUT`/`TTY_ERR` — an ordinary terminal program then
draws in the popup as it would anywhere, and reaches the caller only where it
names a descriptor. That is `run-in-popup exec`, which is this layer used
directly: the user's command as the spec, the process's own three streams as the
endpoints, `KeepStdio` on, and nothing layered on top.

The payload's stdin is the one stream no `Wait` waits for — its relay sits in a
read on the source it was given, which only whoever owns that source can end.

The two exchanges layer a protocol on that. `PinentryLauncher.Call(ctx)` is the
pinentry proxy; it needs a `PopupLauncher` whose `Backend` also implements
`TTYHandshaker`, since the popup has to report the terminal it runs on.
`JsonIpcLauncher[In, Out].Exec(ctx, v)` is the JSON round trip: it returns a
`*JsonIpcConn[In, Out]` whose `Results()` yields the `Out` values decoded from the
payload's stdout — drain it, the payload blocks on its own stdout otherwise — and
whose `Wait` reports how the exchange ended. Input travels one of two ways: the
launcher's `AddPayload` marshals the launch-time value into the popup's command
line, or, without one, the payload's stdin becomes a FIFO and `Send` carries the
values.

A `Backend` itself is small: `Name`, `Launch` and `Prepare`. `Prepare` is where a
backend fixes up multiplexer state a popup would otherwise break — the tmux
de-zoom above is its one implementation — and returns a restore func the launch
runs when the popup is released. `TTYHandshaker` extends it with
`NewTTYHandshake`, built per backend because the popup mechanism decides how the
payload learns the FIFO paths.

Nothing here waits for the popup to be gone. `PopupCommand.Wait` returns once the
popup *launcher* has exited and the output streams it was handed endpoints for
have ended — and the launcher exiting is not the payload finishing: the
floating-pane mechanisms return as soon as the pane exists, so a caller that
needs to know when the payload is done has the payload tell it, as both exchanges
do over their own FIFOs. The restore can therefore land on a live floating pane,
which tmux answers by pulling that pane out of its float and into the layout —
not by crashing. That is the guarantee this relies on; it is not an ordering
guarantee.

`PopupCommand.WaitStreams` waits the same way but lets those streams say how the
launch went: every one of them running to its end is a success, however the
launcher then exited. That is what a caller relaying a payload's output wants,
since `tmux display-popup`'s launcher carries the payload's exit status — `Wait`
would call a payload that exited non-zero a failed launch, while the
floating-pane mechanisms, whose launcher is long gone by then, would call the
same run a success. A launcher failure is still reported when a stream failed
too: a popup that died is why the stream ended, and explains it better.

### But why?

- Sometimes I ssh into my remote machine from somewhere no GUI is supported
- Calling pinentry-curses from lazygit called from neovim breakes terminal state.

calling pinentry-curses from tmux popup prevents this breakage.

Happy vibe coding!
