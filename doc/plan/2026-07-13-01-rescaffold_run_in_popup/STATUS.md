# Status — rescaffold as run-in-popup

**State: planned, ready to implement.** All open questions resolved
(DECISION.md D0–D9); implementation not started.

2026-07-28 amendment: tmux 3.7b crashes the server when a floating pane is
created while a pane is zoomed (fixed in unreleased 3.7c; `display-popup`
unaffected). Resolved as D7–D9: `Backend` gains a `Prepare`/restore seam in
step 4 (no-op for both backends here); the future `tmux-floating-pane`
backend implements version-gated de-zoom with best-effort re-zoom.

## Checklist (mirrors PLAN.md steps)

- [ ] 1. Scaffold canonical helpers + `cmd/run-in-popup` skeleton (go-edit-cobra)
- [ ] 2. Move `PINENTRY_USER_DATA` parsing to `pkg/runinpopup/userdata.go`
- [ ] 3. Port `internal/popup.CallPinentry` → `pkg/runinpopup/pinentry.go` (panic → error)
- [ ] 4. Backend abstraction (`tmux-popup`, `zellij`) + generic `Run` primitive
- [ ] 5. Wire cobra `pinentry` subcommand (`--backend` + auto-detect)
- [ ] 6. Rewrite legacy shims over `pkg/runinpopup` (+ deprecation notice on stderr)
- [ ] 7. Delete `cmd/internal/preprocess` + `internal/popup`, `go mod tidy`
      (do NOT touch `internal/pickentry` — user removes it via force-commit)
- [ ] 8. Update README.md

## Blocked on

Nothing.

## Next action

Start step 1: invoke the go-edit-cobra skill and scaffold the canonical
helpers + `cmd/run-in-popup` skeleton.
