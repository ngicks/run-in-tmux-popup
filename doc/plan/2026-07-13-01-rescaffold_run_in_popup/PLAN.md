# Rescaffold as `run-in-popup` with pluggable popup backends

Rescaffold the whole project with the `go-edit-cobra` canonical layout: a new
cobra entrypoint `run-in-popup` with selectable backends (tmux, zellij), core
logic centered in `./pkg/runinpopup` for import by other Go modules, and the
old entrypoints preserved as thin backward-compatible shims.

## Goal / success criteria

- `go build ./...` and `go test ./...` pass.
- New binary `run-in-popup` exists under `cmd/run-in-popup/` following the
  go-edit-cobra canonical layout (main.go + commands/ + version/config
  subcommands + helper packages under `internal/`).
- Backend (tmux-popup vs zellij floating pane) is selectable at the new
  entrypoint.
- All popup/pinentry logic lives in `pkg/runinpopup` with an importable,
  CLI-free API (context-first, DI, errors-as-values — no `panic` for normal
  failures, no env reads inside the service except where that IS the purpose,
  e.g. `PINENTRY_USER_DATA` parsing helpers that take the string as input).
- `tmux-popup-pinentry-curses` and `zellij-popup-pinentry-curses` still build
  and behave identically from the outside (same env contract, same args, same
  popup behavior) but are thin wrappers over `pkg/runinpopup`, each emitting a
  deprecation notice on stderr at startup.
- Clutter removed (see Scope).

## Scope

- Scaffold `cmd/run-in-popup/` via the go-edit-cobra skill (scaffold mode is
  N/A since `cmd/` is populated — treat as edit-mode "close variant" and add
  the canonical binary + helpers without force-migrating legacy mains).
- Create `pkg/runinpopup/` and move/redesign logic from `internal/popup` and
  `cmd/internal/preprocess` into it.
- Introduce a `Backend` abstraction with tmux and zellij implementations.
- Rewrite legacy `cmd/{tmux,zellij}-popup-pinentry-curses/main.go` as shims.
- Add canonical helper packages: `internal/cmdsignals`, `internal/loggerfactory`,
  `internal/templateutil`, `internal/versioninfo`, `internal/cmd/release`
  (copied via the skill's helper catalog).
- Update README.md for the new entrypoint while keeping legacy docs.

## Non-goals

- Changing the `PINENTRY_USER_DATA` wire format (legacy contract stays).
- New popup backends beyond `tmux-popup` and `zellij` — `tmux-floating-pane`
  is planned later; the name-space and abstraction must accommodate it, but it
  is not implemented now.
- A CLI `run` subcommand (the library primitive exists; CLI exposure comes
  later).
- Touching `internal/pickentry` — the user removes it separately
  (force-commit).
- Renaming the module path (decided: keep `github.com/ngicks/run-in-tmux-popup`).

## Context (current state)

- `cmd/tmux-popup-pinentry-curses/main.go` — plain main; parses
  `PINENTRY_USER_DATA` via `preprocess.Do("tmux")`, sets `$TMUX` from session
  meta, generates random prefix/suffix as FIFO-hijack countermeasure, calls
  `popup.CallPinentry` with a tmux `popup -c <client> -E ...` command builder.
- `cmd/zellij-popup-pinentry-curses/main.go` — same shape; zellij
  `run --floating --pinned ...` builder, no client targeting (zellij can't),
  no prefix/suffix validation.
- `cmd/internal/preprocess/preprocess.go` — `ParsePinentryUserData` (colon-split
  of `PINENTRY_USER_DATA`: kind:path:session_id:client_id:session_meta:rest)
  and `Do(name)` — a 7-return-value setup function mixing ctx/timeout, signal
  handling, tempdir, debug-mode logger, and env parsing. Prime rescaffold
  target.
- `internal/popup/popup.go` — `CallPinentry(ctx, logger, tempdir,
  buildPopUpCmd, validateTtyStr, pinentryPath, pinentryArgs)`: creates tty/done
  FIFOs, launches the popup command, reads the popup's tty from the FIFO,
  spawns pinentry with stdin interception rewriting `OPTION ttyname=` to the
  popup tty, signals done. Core logic; panics in several normal-failure paths
  (Mknod, OpenFile, os.Pipe) that must become returned errors.
- `internal/pickentry/` — 820-line bubbletea fuzzy-picker TUI. Its only
  consumer `cmd/pickentry` was deleted in `8820a26` ("remove unused
  pickentry"). Out of scope: the user removes it themselves via force-commit;
  this plan leaves it (and its bubbletea deps in go.mod) untouched.
- `go.mod` — module `github.com/ngicks/run-in-tmux-popup`, go 1.24.0; deps are
  bubbletea/bubbles/lipgloss/fuzzy (only used by pickentry) + golang.org/x/sys.
  No cobra yet.
- Skill canon: `.claude/skills/go-edit-cobra/` — canonical layout is
  `cmd/<name>/main.go` + `cmd/<name>/commands/{root,version,config,...}.go`,
  helpers under `internal/`, service under `pkg/<name>/` with `pkg/<name>/cli/`
  for presentation.
- `.claude/rules/go-design-preference.md` — no business logic under `./cmd`,
  RunE-only, DI over globals, context first, errors are values.
- **tmux 3.7b bug (user-reported 2026-07-26)**: creating a floating pane while
  a pane in the window is zoomed crashes the tmux server. Fix expected in
  3.7c (unreleased). Affects floating panes only — `display-popup` (the
  mechanism the current `tmux-popup` backend uses) is unaffected (confirmed
  by the user). The SDK must de-zoom before creating a floating pane.

## Approach

### Backend selection (decided — D1)

`--backend` flag on the `pinentry` subcommand (persistent on root if more
subcommands appear later). Valid values: **`tmux-popup`** and **`zellij`** —
named for the popup mechanism, not just the multiplexer, because a
`tmux-floating-pane` backend is planned later. When the flag is omitted,
auto-detect: `PINENTRY_USER_DATA` kind (`TMUX_POPUP` → `tmux-popup`,
`ZELLIJ_POPUP` → `zellij`), else `$TMUX` → `tmux-popup`, else `$ZELLIJ` →
`zellij`, else error listing valid values.

### Library vs CLI surface (decided — D2)

`pkg/runinpopup` exposes a **generic run-in-popup capability** plus the
pinentry proxy built on top of it:

- `Run(ctx, backend, RunOptions)` — open a popup via the backend and execute
  an arbitrary command in it (the generic primitive).
- The pinentry proxy (tty handshake over FIFOs + `OPTION ttyname=` rewrite,
  running pinentry outside the popup) is a specialization layered on the same
  backend popup mechanism.

The CLI exposes **only `run-in-popup pinentry`** for now; a `run` subcommand
can be added later without library changes.

### Target layout

```
cmd/
  run-in-popup/
    main.go
    commands/
      root.go                    # rootCmd() + Execute(ctx) + runRoot
      version.go
      config.go
      pinentry.go                # "pinentry" leaf — the pinentry proxy (OQ2)
  tmux-popup-pinentry-curses/
    main.go                      # compat shim → pkg/runinpopup (tmux backend)
  zellij-popup-pinentry-curses/
    main.go                      # compat shim → pkg/runinpopup (zellij backend)
internal/
  cmdsignals/                    # from skill helper catalog
  loggerfactory/
  templateutil/
  versioninfo/
  cmd/release/
pkg/
  runinpopup/
    version.go                   # const Version
    config.go                    # Config/PartialConfig/LoadConfig (OQ5)
    userdata.go                  # PinentryUserData + ParsePinentryUserData
                                 #   (moved from cmd/internal/preprocess)
    backend.go                   # Backend interface + lookup by name + auto-detect
    backend_tmux_popup.go        # "tmux-popup" backend + prefix/suffix tty guard
    backend_zellij.go            # "zellij" backend (floating pane)
    run.go                       # Run(ctx, backend, RunOptions) — generic primitive
    pinentry.go                  # pinentry proxy service (from internal/popup)
    cli/
      config.go                  # RenderConfig
```

`cmd/internal/preprocess` and `internal/popup` are deleted after migration.

### Backend abstraction (sketch, refined during implementation)

The backend's job is "open a popup running this command with this env"; the
generic `Run` and the pinentry proxy both build on it:

```go
// pkg/runinpopup/backend.go
type PopupSpec struct {
    Env     map[string]string // injected into the popup (tmux: -e; zellij: via shell)
    Command []string          // what to execute inside the popup
}

type Backend interface {
    // Name reports the backend name ("tmux-popup", "zellij").
    Name() string
    // PopupCommand returns the argv that opens a popup executing spec.
    PopupCommand(spec PopupSpec) (path string, args []string)
    // Environ returns process-env adjustments needed before launching
    // (e.g. tmux-popup needs $TMUX set from session meta).
    Environ() []string
}
```

The pinentry-specific tty handshake (the FIFO shell snippet and its
validation, including the tmux-popup random prefix/suffix hijack guard) lives
in the pinentry proxy layer, parameterized per backend where behavior differs.
Exact split between `Backend` and the proxy is refined in step 4 — the
constraint is that `Run` (arbitrary command) and the pinentry handshake share
one popup-launching mechanism.

### Zoomed-pane handling (tmux 3.7b crash workaround — decided, D7–D9)

tmux 3.7b crashes the server when a floating pane is created while a pane is
zoomed (fixed in unreleased 3.7c; `display-popup` unaffected). **Seam only in
this plan**: the `Backend` abstraction grows a pre-launch state-adjustment
hook so a backend can fix up multiplexer state before the popup command runs;
the actual de-zoom ships with the future `tmux-floating-pane` backend:

```go
// Prepare adjusts multiplexer state that would break (or crash) popup
// creation — e.g. de-zoom for tmux floating panes — and returns a restore
// func to undo the adjustment after the popup closes. Both are no-ops for
// backends that need nothing.
Prepare(ctx context.Context) (restore func(context.Context) error, err error)
```

`Prepare` is a no-op for both backends implemented here (`tmux-popup`,
`zellij`). Decided semantics for the future `tmux-floating-pane`
implementation (recorded so the seam's contract is fixed now):

- **Version-gated (D9)**: de-zoom only when `tmux -V` reports a version
  affected by the bug (< 3.7c). If the version string cannot be parsed,
  assume affected and de-zoom defensively — a spurious de-zoom is flicker;
  a missed one is a server crash.
- **De-zoom mechanics**: query `#{window_zoomed_flag}` via
  `display-message -p`; run `resize-pane -Z` only if zoomed.
- **Restore best-effort (D8)**: the returned restore func re-zooms the
  previously zoomed pane after the popup closes; restore failures are
  logged, never fatal.

Constructors `NewTmuxPopupBackend(...)`/`NewZellijBackend(...)` take explicit
parameters (binary path, session/client ids, shell) rather than reading env —
callers (cobra command, legacy shims) feed them from `PINENTRY_USER_DATA`
or flags.

`CallPinentry` becomes a function taking `(ctx, Backend, PinentryOptions)`
where the options carry logger, tempdir, pinentry path, pinentry args, and
timeouts (currently hardcoded: 2 min overall, 20 s tty read, 1 s done write).
All `panic`s in normal failure paths become returned errors.

### Rejected alternatives

- **Separate package per backend** (`pkg/runinpopup/tmux`, `.../zellij`):
  overkill for ~60 lines each; single package with `backend_*.go` files keeps
  the import story simple for external consumers.
- **Keeping legacy mains byte-identical** (untouched): contradicts "logic
  centered in pkg/runinpopup" — the shims must delegate or the logic is
  duplicated.
- **cobra for the legacy binaries**: they are argv-passthrough proxies invoked
  by gpg-agent; cobra flag parsing would mangle pinentry args. They stay plain
  mains.

## Implementation steps

Each step leaves `go build ./...` green.

1. **Scaffold canonical helpers + new binary skeleton** (go-edit-cobra skill,
   edit-mode): add cobra to go.mod; copy `internal/cmdsignals`,
   `internal/loggerfactory`, `internal/templateutil`, `internal/versioninfo`,
   `internal/cmd/release` from the skill helper catalog; create
   `cmd/run-in-popup/main.go` + `commands/{root,version,config}.go`;
   create `pkg/runinpopup/{version,config}.go` + `pkg/runinpopup/cli/config.go`.
   Verify: `go run ./cmd/run-in-popup version` works.
2. **Move userdata parsing**: `pkg/runinpopup/userdata.go` gets
   `PinentryUserData` + `ParsePinentryUserData(s string)` (takes the string;
   no env read inside). Port `cmd/internal/preprocess/preprocess_test.go`.
3. **Port core logic**: `pkg/runinpopup/pinentry.go` from
   `internal/popup.CallPinentry`, converting panics to errors and hoisting
   hardcoded timeouts/paths into a `PinentryOptions` struct.
4. **Backend abstraction + generic Run**: `backend.go` (interface, lookup by
   name, auto-detect) + `backend_tmux_popup.go` + `backend_zellij.go`,
   absorbing the per-binary command builders, the tmux prefix/suffix guard,
   and `$TMUX` env handling from the current mains; `run.go` with the generic
   `Run(ctx, backend, RunOptions)` primitive, with the pinentry proxy layered
   on the same mechanism. Includes the `Prepare`/restore seam for the tmux
   3.7b zoomed-pane workaround (seam only, no-op for both backends — D7).
5. **Wire the cobra `pinentry` subcommand**: `--backend tmux-popup|zellij`
   with auto-detect when omitted, `--pinentry` path flag, passthrough args
   after `--`; RunE builds the backend + options from
   flags/config/`PINENTRY_USER_DATA` and calls the service. No generic `run`
   subcommand yet.
6. **Rewrite legacy shims**: `cmd/{tmux,zellij}-popup-pinentry-curses/main.go`
   become ~30-line mains: parse env, build the matching backend, call the
   service; identical external behavior (exit codes/panic messages may become
   cleaner error prints — acceptable). Each shim prints a **deprecation
   notice** at startup pointing at the `run-in-popup pinentry` replacement —
   to **stderr only**, never stdout, since the pinentry Assuan protocol runs
   over stdin/stdout and gpg-agent invokes these non-interactively.
7. **Delete clutter**: remove `cmd/internal/preprocess` and `internal/popup`;
   `go mod tidy`. (`internal/pickentry` is NOT touched here — the user removes
   it themselves with a force-commit.)
8. **Docs**: update README.md — new `run-in-popup` usage first, legacy
   binaries documented as **deprecated** (still working, with the replacement
   command shown and a note about the startup notice on stderr).

## Testing & verification

- Port existing tests: `preprocess_test.go` → `userdata_test.go`.
  (`internal/pickentry` tests are untouched — user handles removal.)
- New unit tests: backend command construction (`PopupCommand` argv for both
  backends), tmux prefix/suffix `ValidateTTY` accept/reject.
- `go build ./...`, `go vet ./...`, `go test ./...`.
- Manual smoke (needs a live tmux/zellij session — user-assisted):
  `PINENTRY_USER_DATA=... run-in-popup pinentry` inside tmux, and via
  gpg-agent with the wrapper script pointing at a legacy shim.
- Run `go-check-outdated-patterns` and `go-review-checklist` skills after
  editing.

## Risks

- **Behavioral drift in legacy shims** — gpg-agent invokes them non-interactively;
  a subtle env/exit-code change breaks pinentry silently. Mitigate by keeping
  the env contract identical and smoke-testing via gpg-agent.
- **FIFO/tty logic is timing-sensitive** — porting `CallPinentry` must not
  reorder FIFO open/read/write sequencing; port mechanically first, refactor
  second.
- **zellij still can't target a client** — backend interface must not force
  client-id semantics that zellij cannot honor.
- **tmux 3.7b zoomed-pane crash** — creating a floating pane with a zoomed
  pane crashes the tmux server until 3.7c. Any future `tmux-floating-pane`
  backend (and its `Prepare` implementation) must de-zoom first; getting this
  wrong takes down the whole tmux server, not just the popup.

### Configuration (decided — D5)

Full go-edit-cobra canon: `pkg/runinpopup/config.go` with
`Config`/`PartialConfig`/`LoadConfig` and a `config` subcommand. Fields:
pinentry path (default `/usr/bin/pinentry-curses`), default backend, and
timeouts (overall — currently 2 min, tty read — 20 s, done write — 1 s).
Flags overlay config per the skill's `PartialConfig.Apply` model.

## Open questions

(none — all resolved; see DECISION.md.)
