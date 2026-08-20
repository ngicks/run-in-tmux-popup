# DECISION — exec becomes a plain stream bridge

## Exec stays as a stream bridge (user, 2026-08-19; supersedes the retracted full deletion)

The user first chose deleting the exec exchange whole, then retracted
it at the idea gate: "execを丸ごと削除を撤回し、残すことにする。execは
popup内でアプリを実行し、stdout, stderrをつなぐだけにする。"
Operative clauses:

- `run-in-popup exec` remains a CLI command.
- The popup runs the user's command **directly** — the `exec-payload`
  wrapper process is deleted, and with it the constraint it imposed on
  the popup command.
- exec connects the command's stdout and stderr to the caller's stdout
  and stderr; that is all it does ("つなぐだけ").
- The `ExecResult` JSON report and its wire format are deleted.

## stdout-is-JSON is the library contract (user, 2026-08-18/19)

The JSON exchange lives in `JsonIpcLauncher[In, Out]` only: its
documented contract is that the popup command writes JSON-encoded `Out`
values to its stdout. The exec CLI is no longer its consumer.

## Delete now, maybe re-add later (user, 2026-08-19)

A convenience converting an arbitrary command's stdout/exit status into
JSON (an exec-shaped helper over the JSON contract) may be reintroduced
by a later refactor. This plan deliberately does not design or stub it.

## Sub-plan placement (user, 2026-08-19)

This work is a sub-plan of `doc/plan/2026-08-17-refactor_batch_1/`
(`sub/01-remove_exec_exchange/`), not a new dated top-level plan — the
user chose "implement as a sub-plan of the existing one". The directory
slug predates the retraction and is kept for continuity.

## runworkspace.Open always creates the directory (user, 2026-08-19)

Orthogonal to the exec bridge, decided in the same session: an Open
that creates nothing unless debug is unnatural, so os.MkdirTemp moves
to the top of Open and every run gets its directory made there, handed
to the launch as caller-owned. Close now settles the fate fork instead:
an ordinary run's directory is removed (with whatever the launch left
in it), a debug run's is kept with its log.txt. This supersedes the
parent batch's "non-debug creates nothing, the launch owns the
directory" shape from the legacyshim/runworkspace slice.

## Config.DefaultBackend renamed to Config.Backend (user, 2026-08-19)

Orthogonal to the exec bridge, decided in the same session: the field's
name lied — the --backend flag is merged into it before use, so what
resolveRuntime reads is the flag-overridden selection, not a default.
The user chose renaming the field itself over separating the flag into
its own input or renaming only locals: `Config.DefaultBackend` →
`Config.Backend`, JSON/YAML key `default_backend` → `backend`, env
`RUN_IN_POPUP_DEFAULT_BACKEND` → `RUN_IN_POPUP_BACKEND`. A deliberate
pre-1.0 config-schema break; precedence semantics (flag > config >
detection, explicitly-empty flag re-detects) are unchanged.

## tmux tty-handshake guard secrets removed (user, 2026-08-19)

Orthogonal to the exec bridge, decided in the same session: the
per-popup SEC_PREFIX/SEC_SUFFIX wrapping of the announced tty in the
tmux handshake is unnecessary — the FIFOs are mode-0600 files in a
mode-0700 workspace, so filesystem permissions already decide who can
speak on them; the secrets re-checked the same boundary. Both tmux
backends now announce the tty as-is (ValidateTTY nil), matching zellij,
whose "guard would be theater" contrast comment is retired with it.

## Exit code is the exchange's status (user via "Continue", 2026-08-19)

`run-in-popup exec` exits 0 when the bridge worked and 1 on
launch/exchange failure; the inner command's exit status is not
reported. Adopted as the recommended default when the user answered
"Continue" to the gate summary presenting it. Rationale: with the
payload wrapper gone, only tmux display-popup's launcher even carries
the inner status (floating-pane/zellij launchers return early), so
forwarding it would behave differently per backend. Rejected:
forwarding the status where the backend happens to provide it.

## Env travels via a sourced env file where the multiplexer has no env flag (user, 2026-08-19)

Orthogonal to the exec bridge, decided in the same session. Context:
zellij has no `-e`-style env injection flag, so its client currently
exports PopupSpec.Env inline in the payload argv (`export K=V; …`),
visible to every process via ps for the pane's lifetime. The values
are configuration, not secrets — but there is no reason to expose them
when the workspace already offers a better channel. Decision (user:
"zellij or some other impl without -e flag"): backends whose
multiplexer lacks an env flag write the env to a mode-0600 shell file
in the launch workspace and have the popup command source it; the argv
then carries only the file path. The workspace is mode-0700, the same
protection level as the FIFOs beside it. tmux keeps `-e` — it has a
real flag, and uniformity was explicitly not chosen. Rejected: the
uniform launcher-owned env file across all backends.

## Bridge takes all three stdio; the popup TTY moves to fd 3/4/5 + TTY_* env (user, 2026-08-20; amends the bridge shape)

The gate-confirmed shape (stdin = popup TTY, stdout/stderr bridged) was
retracted by the user: redirecting stdout/stderr away from the pane's
pts prevents the command from controlling the floating pane at all.
New shape: the command's fd 0/1/2 are all bridged to the caller's
stdin/stdout/stderr over FIFOs, and the popup's terminal is handed to
the command as fd 3 (read), fd 4 (write) and fd 5 (write), captured
via $(tty) before the stdio redirections displace it. The same
terminal is also named in TTY_IN/TTY_OUT/TTY_ERR environment variables
— three variables, not one, because Windows cannot inherit arbitrary
fds and splits its console into CONIN$/CONOUT$; on POSIX all three
hold the same pts path. The fd handoff is shell redirection inside the
pane's own command line, so it is multiplexer-agnostic (works under
zellij exactly as under tmux). When the popup has no tty the fds and
variables are simply absent rather than failing the launch.

## Corrected: stdio stays on the pts; the CALLER bridge rides fd 3/4/5 (user, 2026-08-20; supersedes the previous fd-3/4/5 entry)

The previous entry inverted the user's intent, and the shipped shape
made `exec -- htop` draw on the calling terminal while the popup stayed
blank. Corrected shape, confirmed by the user: the popup command's
stdio 0/1/2 stay on the popup's pts, so any ordinary TUI draws in the
popup with no convention to learn. The bridge to the caller attaches as
extra descriptors instead — fd 3 reads what the caller piped in, fd 4
writes to the caller's stdout, fd 5 to its stderr — and TTY_IN/TTY_OUT/
TTY_ERR carry the FIFO *paths* so a platform or program that cannot
inherit descriptors can open them by name. The prior $(tty)-capturing
prefix is retired with the inversion: the pts needs no handoff when it
was never taken away. Pipe workflows through the popup are opt-in on
the command side (e.g. `fzf </dev/fd/3 >/dev/fd/4`); JsonIpc and
pinentry launches keep their existing stdio-redirect semantics.

## Popup geometry mimics tmux's display-popup flags (user, 2026-08-20)

The abstracted popup location/size option adopts tmux's vocabulary
rather than a new anchor scheme ("mimic tmux's flag"): PopupSpec gains
X, Y, Width and Height as strings — a bare number is cells, "N%" is a
percentage of the terminal, and X/Y additionally accept tmux's
position specifiers (C centre, R right, P pane, M mouse, W window
position, S status line) which pass through to tmux untranslated.
Empty means the backend's own default, producing today's argv
byte-for-byte. zellij maps cells and percentages onto
--x/--y/--width/--height and rejects the tmux-only specifiers with an
error naming the backend; the tmux floating-pane backend mirrors
display-popup's flags onto new-pane, to be confirmed against the fork
by the tagged integration suite. Exposure: the library spec plus exec
flags --x, --y, -w/--width and --height (no -h shorthand — cobra
reserves it for --help). Config keys and the pinentry surface are
deferred. Rejected: an anchor+offset abstraction; raw per-backend
pass-through fields.

## Amendment: the fork's new-pane geometry flags differ (verified 2026-08-20)

The geometry entry above assumed new-pane mirrors display-popup's
flags; probing the actual tmux 3.7b fork (`list-commands` on a private
server in this environment) disproved it. new-pane takes `-x width`,
`-y height`, `-X x-position`, `-Y y-position` — lowercase x/y are SIZE
there, position is uppercase — while display-popup keeps `-x/-y`
position and `-w/-h` size. The floating-pane backend therefore maps
Width→-x, Height→-y, X→-X, Y→-Y. Whether -X/-Y accept the same
position specifiers as display-popup remains for the tagged
integration suite to confirm.

## Geometry X/Y mean the top-left corner everywhere (user, 2026-08-20; amends the geometry entry)

The verbatim pass-through made the same numeric Y mean different
things: display-popup anchors its bottom edge at y, while the fork's
new-pane -Y (and zellij's --y) place the top-left corner. The user
ruled the divergence out ("Using different meaning of same numeric
value is bad. Re-interpret them"): the abstract X/Y are defined as the
popup's top-left corner on every backend. new-pane and zellij pass
through unchanged; the display-popup client translates numeric Y to
tmux's bottom anchor as y+height — cells with cells, percent with
percent (tmux's own rounding may drift one cell). A numeric/percent Y
with no Height, or with mixed units, is an error telling the caller to
give --height in the same unit: assuming tmux's default half-terminal
height cannot be done in cells without knowing the terminal size, and
guessing places the popup wrong silently. Position specifiers
(C/R/P/M/W/S) stay semantic and pass through untranslated.
