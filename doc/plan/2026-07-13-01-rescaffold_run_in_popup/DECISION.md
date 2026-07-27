# Decision log — rescaffold as run-in-popup

## Decided

### D0. Core logic lives in `pkg/runinpopup`; legacy binaries become shims

- **Choice**: All popup/pinentry logic moves to `pkg/runinpopup` (importable by
  other modules); `cmd/{tmux,zellij}-popup-pinentry-curses` are rewritten as
  thin plain-`main` shims over it; the new `run-in-popup` binary is cobra-based
  per the go-edit-cobra canon.
- **Rationale**: Stated directly in the user's request; also matches
  `.claude/rules/go-design-preference.md` (no business logic under `./cmd`).
- **Rejected**: keeping legacy mains untouched (duplicates logic); cobra for
  the legacy shims (would mangle passthrough pinentry argv).

### D1. Backend selection UX (OQ1) — resolved 2026-07-13

- **Choice**: `--backend` flag with auto-detect when omitted. Values are
  **`tmux-popup`** and **`zellij`** — named for the popup mechanism, not the
  multiplexer, because a `tmux-floating-pane` backend will be added later.
  Auto-detect order: `PINENTRY_USER_DATA` kind, then `$TMUX`, then `$ZELLIJ`.
- **Rationale**: zero-config for gpg-agent wrapper scripts, explicit override
  available; mechanism-based names keep room for multiple backends per
  multiplexer.
- **Rejected**: backend subcommands (doubles surface per feature);
  explicit-only flag (inconvenient); plain `tmux` as a value (ambiguous once
  tmux-floating-pane exists).

### D2. Command surface (OQ2) — resolved 2026-07-13

- **Choice**: `pkg/runinpopup` supports a generic `Run` (arbitrary command in
  a popup) with the pinentry proxy layered on it; the CLI exposes **only**
  `run-in-popup pinentry` (plus version/config) for now.
- **Rationale**: library-first design per the user; the CLI surface stays
  small until a generic `run` command is actually wanted.
- **Rejected**: pinentry-only library (would block programmatic generic use);
  shipping a CLI `run` subcommand now (scope).

### D3. Fate of internal/pickentry (OQ3) — resolved 2026-07-13

- **Choice**: out of scope — the user removes it themselves and force-commits.
  This plan does not touch `internal/pickentry` or its deps.
- **Rejected**: deleting it in this plan (would collide with the user's
  history rewrite); promoting to `pkg/`.

### D4. Module path (OQ4) — resolved 2026-07-13

- **Choice**: keep `github.com/ngicks/run-in-tmux-popup`.
- **Rationale**: no import breakage; repo rename is a separate later decision.
- **Rejected**: renaming to `github.com/ngicks/run-in-popup` now.

### D5. Config depth (OQ5) — resolved 2026-07-13

- **Choice**: full go-edit-cobra canon — config file carrying pinentry path,
  default backend, and the three timeouts (overall 2 m, tty read 20 s, done
  write 1 s); flags overlay config via `PartialConfig.Apply`.
- **Rationale**: matches the skill canon; the hardcoded values in
  `internal/popup` become user-tunable for free.
- **Rejected**: minimal fields (pinentry path only); no config file (deviates
  from canon).

### D6. Legacy entrypoints carry a deprecation notice — resolved 2026-07-13

- **Choice**: `tmux-popup-pinentry-curses` and `zellij-popup-pinentry-curses`
  print a deprecation notice at startup naming the `run-in-popup pinentry`
  replacement. The notice goes to **stderr only** — the pinentry Assuan
  protocol owns stdin/stdout, so writing it there would corrupt the exchange
  with gpg-agent. README marks them deprecated as well.
- **Rationale**: user directive; signals the migration path without breaking
  existing gpg-agent setups.
- **Rejected**: silent preservation (users never learn about the new
  entrypoint); removing the old binaries outright (breaks backward
  compatibility the user explicitly wants).

### D7. Scope of the tmux 3.7b de-zoom workaround (OQ6) — resolved 2026-07-28

- **Context**: tmux 3.7b crashes the server when a floating pane is created
  while a pane is zoomed; fix expected in unreleased 3.7c. The user confirmed
  the bug affects floating panes only — `display-popup` (the current
  `tmux-popup` backend mechanism) is unaffected.
- **Choice**: seam only in this plan — `Backend` gains
  `Prepare(ctx) (restore func(ctx) error, err error)`, a no-op for
  `tmux-popup` and `zellij`; the actual de-zoom ships with the future
  `tmux-floating-pane` backend. The de-zoom semantics (D8, D9) are decided
  now so the seam's contract is fixed.
- **Rationale**: `tmux-floating-pane` remains a non-goal; the crash only
  bites a backend this plan doesn't implement, so the plan's job is to make
  sure the abstraction can express the workaround.
- **Rejected**: shipping the tmux zoom helpers now (dead code until the
  floating backend exists); pulling `tmux-floating-pane` into scope (scope
  expansion); de-zooming for `tmux-popup` defensively (confirmed
  unnecessary).

### D8. Restore zoom after popup closes (OQ7) — resolved 2026-07-28

- **Choice**: restore best-effort — `Prepare`'s returned restore func
  re-zooms the previously zoomed pane after the popup closes; restore
  failures are logged, never fatal.
- **Rationale**: the user zoomed deliberately; silently leaving the window
  unzoomed after every pinentry popup degrades their layout.
- **Rejected**: leaving the window unzoomed (worse UX for no real
  simplicity gain).

### D9. Version-gating the de-zoom (OQ8) — resolved 2026-07-28

- **Choice**: gate on tmux version — de-zoom only when `tmux -V` reports a
  version affected by the bug (< 3.7c). Unparseable version strings are
  treated as affected (de-zoom defensively): a spurious de-zoom is flicker,
  a missed one is a server crash.
- **Rationale**: once 3.7c lands the workaround's unzoom/re-zoom flicker is
  pure cost; gating removes it on fixed versions.
- **Rejected**: unconditional de-zoom (simpler, but permanent flicker after
  the upstream fix).
