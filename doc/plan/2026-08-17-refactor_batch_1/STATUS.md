# STATUS — refactor batch 1

State: **in progress** — step 1 landed 2026-08-18: six hermetic
characterization tests for `callPinentry` (fake handshaking backend +
fake pinentry via the test binary; three unexported stdio seams on
`PinentryOptions`), live tmux tests moved behind `//go:build integration`,
golden table tests for `cli.RenderConfig`/`cli.RenderExecResult`.
Hermeticity verified by running the default suite with unusable
tmux/zellij stubs first in PATH; `go vet` clean with and without the
`integration` tag.

Step 2 landed 2026-08-18 as three commits: internal tmux/zellij clients
(+ shared `internal/shellargv` quoting), backend name vocabulary moved
into `runinpopup/backend` (parent symbols deleted, error bytes verified
identical), and the contract break + launch layer (`Backend.Launch` +
wait-only `PopupHandle` replace `PopupCommand`/`Environ`; exported
`PopupLauncher.Exec` → `PopupCommand` with per-stream "nil = no
allocation" FIFO wiring and `StdoutPipe`/`StderrPipe`; `Run`/`RunOptions`
deleted; `CallExec`/`CallPinentry` now drive the launch layer with
all-nil streams). Both step-1 race findings are fixed as a natural
consequence: the launcher is always reaped and its output buffers are
read only after `Wait` — `go test -race ./...` is green. Noted
deviations: exec's launcher-failure error tail now comes decorated by
the internal client (pinned substrings preserved); launcher teardown is
SIGINT then SIGKILL after 2s; `Prepare` runs after FIFO creation in
`callPinentry` (unobservable). New dep: golang.org/x/sync (errgroup).

Step 3 landed 2026-08-18: `resolveRuntime(inputs, environ)` in
`cmd/run-in-popup/commands/runtime.go` single-homes backend precedence
and `backend.Options` construction; both commands call it. One sketch
deviation, forced by the purity requirement: `runtimeInputs` carries
`Config` + `Overrides` (merged inside the resolver) instead of
`ConfigPath` — `LoadConfig` stays in the commands so the resolver reads
no files/env. The two commands' old resolution paths were verified
statement-identical, so no per-command behavior fork was needed.
Precedence table asserts full error bytes and was verified by mutation.

Step 4 landed 2026-08-18: `JsonIpcLauncher[In, Out]`/`JsonIpcConn` built
on `PopupLauncher`; exec exchange migrated (`Out = ExecResult`, inner
command as launch-time input, result JSON on the payload-stdout FIFO);
`ExecOptions`/`CallExec` deleted; `ExecPayload` loses the result-FIFO
argv parameter; CLI exec constructs the launchers directly with
`WorkspaceOptions{NamePrefix, Retain}`. Notable deviations, all
documented in the code: a leading `\n` liveness probe precedes the
result JSON on the stream (object bytes unchanged); stdin FIFO is
allocated exactly when `AddPayload == nil` (no exchange uses both input
modes; no `CloseSend` yet); rendezvous-failure wording is now the launch
layer's ("the popup did not reach its payload within …") with the
distinction from "without reporting a result" preserved and tested;
`ExecResult.Error` tag is `omitzero` (byte-identical for string).

Open items for later steps: exec debug runs no longer write
`runworkspace`'s log.txt (dir retention works via WorkspaceOptions;
debug-log policy is step 6's call, README.md:104 wording included);
a SIGKILLed caller can leave the popup shell blocked in open(2) on the
stdout FIFO (graceful timeout paths are covered; noted in code).

Next action: step 5 (TTY rendezvous + Assuan transformer + iopipe;
migrate pinentry onto PinentryLauncher, dissolve
PinentryOptions/CallPinentry).

## Checklist

- [x] Step 1 — hermetic test split + pinentry characterization (P0: "An
      Assuan transcript test proves the exact bytes forwarded to pinentry")
- [x] Step 2 — internal clients + vocabulary + contract break (D6: "delegate
      executable operations to those internal clients"; D4: "own backend
      names, enumeration, detection, and construction"; D7: "raw argv and
      process environment are no longer part of the public backend contract";
      D10: "no compatibility period" — old parent-package symbols deleted;
      D9: `Run`/`RunOptions` deleted; D13/D15: `PopupLauncher.Exec(ctx,
      spec, streams)` returns `*PopupCommand` with `Wait` +
      `StdoutPipe`/`StderrPipe`; launcher-owned pipe goroutines connect
      FIFOs to the passed endpoints)
- [x] Step 3 — CLI runtime resolver (D5: "Proceed with the P1 CLI
      runtime/backend-resolution extraction")
- [x] Step 4 — popup session; exec migrated onto `JsonIpcLauncher` with
      `Out = ExecResult` (D1: "must not duplicate the lifecycle"; D15:
      "`Exec(ctx, v In)` … single entry method"; D14 `ExecOptions`/`CallExec`
      removed; D12 retention reported via log)
- [ ] Step 5 — TTY rendezvous + Assuan transformer + iopipe; pinentry
      migrated (D2: "Split TTY acquisition from Assuan rewriting and pinentry
      process execution"; D3: "iopipe … rather than an ad hoc os.Pipe plus an
      unowned goroutine"; D13: "`PinentryLauncher`"; D12 applied to the
      pinentry exchange surface)
- [ ] Step 6 — legacyshim + workspace ownership (D11: "extract an
      `internal/legacyshim` runner … each `main.go` becomes a tiny adapter";
      D1: shims no longer create their own run directories)
- [ ] Step 7 — mechanical exec split (P2: "exported API and JSON are
      byte-for-byte unchanged")
- [ ] Step 8 — config schema source + residue cleanup (P2: "one clear
      test/generation failure at every contract"; P3 residue)

## Open questions

| # | Decision | State |
| --- | --- | --- |
| — | D8 plan structure | resolved 2026-08-18 — single plan |
| — | D9 `Run` fate | resolved 2026-08-18 — removed outright |
| — | D10 vocabulary break form | resolved 2026-08-18 — clean break |
| — | D11 deprecated binaries | resolved 2026-08-18 — kept via `internal/legacyshim` |
| — | D13 API relayering | resolved 2026-08-18 — launch/exchange launcher structs |
| — | D14 `Call*`/options fate | resolved 2026-08-18 — dissolved |
| — | naming veto round | resolved 2026-08-18 — `PopupHandle`; rest stand |
| — | D15 `JsonIpcLauncher` semantics | resolved 2026-08-18 — bidirectional stream; infra on `PopupLauncher` |
| 5 | `AddPayload` first parameter | resolved 2026-08-18 — literal `In` value |
| 6 | single `T` vs `[In, Out]` | resolved 2026-08-18 — `[In, Out any]` |
| 7 | `Call` return shape | resolved 2026-08-18 — connection handle |
| 8 | wiring config carrier | resolved 2026-08-18 — per-`Exec` `PopupStreams`; spec split (corrected: template `PopupSpec` stream-free) |
| 9 | payload stdin form | resolved 2026-08-18 — `PopupStreams.Stdin io.ReadCloser` |
| 10 | launch-time input routing | resolved 2026-08-18 — `Exec(ctx, v In)` + `Start(ctx)` |
| 11 | `PopupCommand` accessors | superseded — see 14 |
| 14 | accessors under "nil = no allocation" | resolved 2026-08-18 — `StdoutPipe`/`StderrPipe` bools + `(io.ReadCloser, bool)` methods |
| 12 | backend-level spec name | resolved 2026-08-18 — `LaunchSpec` |
| 13 | `JsonIpcLauncher.Exec` return | resolved 2026-08-18 — controller; no `Start` |
