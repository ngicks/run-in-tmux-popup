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
