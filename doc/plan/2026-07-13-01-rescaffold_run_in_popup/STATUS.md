# Status — rescaffold as run-in-popup

**State: implemented, verification pending final review.** All open questions
resolved (DECISION.md D0–D9); all 8 steps done. `go build ./...`,
`go vet ./...` and `go test ./...` pass. Still outstanding: the manual smoke
test in a live tmux/zellij session (Testing & verification in PLAN.md), the
`go-check-outdated-patterns` / `go-review-checklist` passes, and a final
review of the whole change.

2026-07-28 amendment: tmux 3.7b crashes the server when a floating pane is
created while a pane is zoomed (fixed in unreleased 3.7c; `display-popup`
unaffected). Resolved as D7–D9: `Backend` gains a `Prepare`/restore seam in
step 4 (no-op for both backends here); the future `tmux-floating-pane`
backend implements version-gated de-zoom with best-effort re-zoom.

2026-07-28 amendment: the service package moved from `pkg/runinpopup/` to the
top-level `runinpopup/` per the updated go-edit-cobra skill, on explicit user
request (the skill's preserve-clause allows migration only that way). Import
path is now `github.com/ngicks/run-in-tmux-popup/runinpopup`. PLAN.md and
DECISION.md keep the `pkg/` spelling as the historical record of what was
decided at the time. `version.go` stays in the service package and
`internal/cmd/release` stays vendored — both carry their own
explicit-request-only clauses and neither migration was requested; the release
tool's version-file glob was widened to find the new location.
(Superseded later the same day — see the next amendment.)

2026-07-28 amendment (supersedes the last two sentences above): the user
explicitly requested the two remaining legacy-variant migrations, so both are
now done.

- The version constant moved from `runinpopup/version.go` to
  `internal/libver/libver.go` (canonical fixed path, file copied verbatim from
  the skill helper). The value `v0.0.1-devel` was carried over unchanged: the
  canon's `v0.0.0-devel` is the initial value for a *new* scaffold, and the
  migration clause says nothing about resetting an existing version.
  `runinpopup.Version` is gone from the public library API (pre-v1,
  user-requested); the package doc comment it carried moved to `run.go`.
- `internal/cmd/release/` is deleted. Releases now run the external tool:
  `go run github.com/ngicks/go-common/tools/bump-libver@latest <release-version>`,
  which rewrites `internal/libver/libver.go`, commits, tags, and pushes.

The project now matches the current go-edit-cobra canon on all three
previously-divergent points (top-level service package, `internal/libver`, no
vendored release code). PLAN.md and DECISION.md keep their original spelling
as the historical record.

2026-07-28 amendment: the `tmux-floating-pane` backend is **implemented**, on
explicit user request. PLAN.md lists it as a non-goal ("New popup backends
beyond `tmux-popup` and `zellij`") and D7 deliberately shipped only the seam —
both stay as the historical record of what was decided then.

The backend honors the contract D7–D9 fixed for it, unchanged:

- `runinpopup/backend_tmux_floating_pane.go` — `tmux new-pane` argv, targeted by
  session (`-t`); `ClientId` cannot be honored, `new-pane` has no
  client-targeting flag. No `-d`: the popup must take the keyboard.
- **D9 version gate**: `Prepare` runs `tmux -V` and de-zooms on anything not
  positively identifiable as ≥ 3.7c. `next-3.8` development builds count as
  affected — their version string pins no commit. Unparseable, empty, and an
  unrunnable `tmux -V` all fall through to the de-zoom path.
- **De-zoom mechanics**: `display-message -p '#{window_zoomed_flag}:#{pane_id}'`,
  then `resize-pane -Z` on that pane id. Failing to read the zoom state on an
  affected tmux aborts before the pane is created (fail closed). Every `Prepare`
  exec carries `Environ()`, or it would inspect the default socket's server
  while the popup opens on another.
- **D8 restore**: re-zooms the remembered pane id — not the session's active
  pane, which may still be the popup. `withPopupPrepared` (run.go) already logs
  restore errors instead of failing the run, so no caller change was needed.

Verified against the local tmux 3.7b on scratch servers (`-L`, never the default
socket): the crash reproduces when a floating pane created over a zoomed pane
exits, and does not when the pane is created after `Prepare`'s de-zoom.
`Backend`'s `Prepare` doc comment and the README's backend section carry the
same contract. Shims untouched.

2026-08-09 amendment: an `exec` subcommand is **implemented**, on explicit user
request, together with the result transport it needs. PLAN.md scopes the work to
the pinentry proxy plus a generic `Run` primitive (step 4), and `Run`'s doc
comment says a payload that must report back has to arrange its own signalling —
which was true of the pinentry FIFO handshake and is what this adds a second,
general answer to. PLAN.md and DECISION.md stay as the historical record.

- `runinpopup/exec.go` — `CallExec` opens a popup running this binary again as a
  hidden `exec-payload` subcommand, and reads one `ExecResult` (JSON: `command`,
  `exit_code`, `stdout`, `stderr`, `error`) back over a FIFO in the caller's
  temp dir. `ExecPayload` is the popup half: it tees the command's output to the
  popup terminal while capturing it, with stdin on the popup's tty.
- **Why not layered on `Run`**: `Run` returns when the *launcher* exits, which
  for both floating-pane backends is while the payload is still running. `exec`
  has to return when the command is done, so it waits on the FIFO instead — the
  same reason `CallPinentry` does not use `Run` either.
- **Rendezvous, not `O_RDWR`**: unlike the pinentry FIFOs, the payload opens its
  write end (`O_WRONLY|O_NONBLOCK`, retrying `ENXIO`) *before* running anything
  and the caller opens read-only, so the FIFO's EOF is the payload's death
  certificate rather than something the caller's own writer masks. Only the
  rendezvous is on a clock (`ExecOptions.StartupTimeout`, 30s, passed to the
  payload in its argv); the command itself runs unbounded under ctx.
  `Config.Timeouts` is not consulted — it sizes a pinentry prompt.
- **Exit status**: the caller exits 0 whenever the exchange worked, with the
  command's status inside the JSON. The popup process exits *as* the command
  (signal death mapped to 128+signal), through an unexported `exitCodeError` the
  entry point unwraps, so nothing under `./cmd` calls `os.Exit` from a `RunE`.

Verified end to end against the local tmux 3.7b on a scratch server for
non-zero, zero, unstartable and signal-killed commands, and by unit tests for
the failure paths a live popup cannot easily produce (payload that never
connects, payload that dies after connecting, cancellation in either phase).

## Checklist (mirrors PLAN.md steps)

- [x] 1. Scaffold canonical helpers + `cmd/run-in-popup` skeleton (go-edit-cobra)
- [x] 2. Move `PINENTRY_USER_DATA` parsing to `pkg/runinpopup/userdata.go`
- [x] 3. Port `internal/popup.CallPinentry` → `pkg/runinpopup/pinentry.go` (panic → error)
- [x] 4. Backend abstraction (`tmux-popup`, `zellij`) + generic `Run` primitive
- [x] 5. Wire cobra `pinentry` subcommand (`--backend` + auto-detect)
- [x] 6. Rewrite legacy shims over `pkg/runinpopup` (+ deprecation notice on stderr)
- [x] 7. Delete `cmd/internal/preprocess` + `internal/popup`, `go mod tidy`
      (do NOT touch `internal/pickentry` — user removes it via force-commit)
- [x] 8. Update README.md

Step 7 note: `internal/pickentry` was already gone before this step ran — the
user removed it (with `cmd/pickentry`) in commit `6ed9be4`. Nothing to leave
untouched, so `go mod tidy` legitimately dropped its now-unimported deps
(bubbletea, bubbles, lipgloss, sahilm/fuzzy, and their indirects, plus
golang.org/x/sys, which nothing imports either). go.mod keeps caarlos0/env,
go-common/contextkey and cobra.

## Blocked on

Nothing.

## Next action

Review the change (`go-check-outdated-patterns`, `go-review-checklist`, and a
full diff review), then run the manual smoke test inside a live tmux/zellij
session and via gpg-agent with a wrapper script.
