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
