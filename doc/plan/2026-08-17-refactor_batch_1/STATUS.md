# STATUS — refactor batch 1

State: **done** — all eight steps implemented and committed 2026-08-18;
final gate passed: full suite (`-race -count=2 ./...` included),
hermeticity with unusable tmux/zellij stubs, both vet tag sets,
golangci-lint clean; multi-focus review verdict approve-with-nits, no
blocking defects, three one-sentence doc fixes applied. Remaining
follow-ups (recorded, deliberately not done in this batch): the stdin
relay's error is unreachable for a streamed `JsonIpcLauncher` exchange
(`Send` sees a bare `io.ErrClosedPipe`, `Wait` can stay nil — needs the
conn to consume the endpoint waits); `launch.go` at 569 lines wants the
same by-responsibility split `exec.go` got; nothing automates
`go test -tags integration`; `JsonIpcConn` has no `CloseSend`; a
SIGKILLed exec caller can leave the popup shell blocked in open(2);
README omits `config --format` and the `version` subcommand
(pre-existing). The live pinentry-curses gap is closed: 2026-08-22
brought `runinpopup/pinentry_live_test.go` (build tag `integration`),
which runs a real pinentry-curses in a real tmux popup on a private
server; still nothing automates the tagged runs.

Step 1 landed 2026-08-18: six hermetic
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

Step 5 landed 2026-08-18: pinentry split into TTY rendezvous
(`ttyhandshake.go`), pure `rewriteAssuanTTY` transformer (`assuan.go`),
pinentry process runner, and a coordinator testable with fakes;
`PinentryLauncher.Call` on `PopupLauncher`; iopipe (go-common v0.0.1)
controllers at the OS-stdio boundary with the primary-error-wins close
handling; `PinentryHandshake(r)` renamed `TTYHandshake(r)` per the
recorded decision; `PinentryOptions`/`CallPinentry` deleted. Pinentry
debug runs still get `runworkspace`'s dir + log.txt (handed to the
launcher as caller-owned `Dir`); shims minimally ported. Notable
deviations, documented in code: pinentry's stdout/stderr were made
os/exec pipes rather than inherited *os.File fds (Assuan bytes verified
identical) — since amended, see below; cancellation is SIGINT + 2s grace
instead of immediate SIGKILL; the seamed stdin is no longer closed by
the library; per-line debug logs replaced by one `pinentry started` log
naming the tty. An iopipe quirk was recorded in stdio.go at the time: a
Writer's completion channel can report nil for a failing write, so
output failures surfaced through cmd.Wait and input failures through
close(). That note went away with the output relays it described.

Amendment landed 2026-08-22 (recorded in DECISION.md): pinentry's
stdout and stderr are handed to the child as real files again, so the
child writes on the descriptor gpg-agent reads and this process is no
longer the one writing to its own fd 1 and 2. The relayed shape let a
gpg-agent that dropped the connection right after BYE break the pipe
under the proxy, and the fatal SIGPIPE killed it before it could dismiss
the popup — the popup then stayed on screen after the user had answered
the prompt. Only the Assuan input keeps its iopipe controller, for the
reason it was introduced: the exchange needs an end it may close on a
read only gpg-agent can end. `processStdio` is `assuanInput`
accordingly.

Steps 6 and 7 landed 2026-08-18 (parallel workers, disjoint files).
Step 7: `exec.go`/`exec_test.go` split into
`exec_types.go`/`exec_payload.go`/`exec_status.go` + per-half test
files and `main_test.go` (the shared TestMain dispatch); `go doc -all`
output byte-identical; no `exec_caller.go` — the caller half is the
generic `JsonIpcLauncher` in `jsonipc.go`, which the types header
points at. Step 6: `internal/legacyshim.Shim.Run` runs both deprecated
binaries (mains are ~40-line adapters); `internal/runworkspace` shrank
to debug-log policy via `Open(prefix, debug, fallback)` returning
`WorkspaceOptions` + logger — non-debug creates nothing (launch owns
the dir), debug keeps dir + log.txt; both recorded defects fixed
(rollback on log-open failure, Close error surfaced at all call
sites without displacing a primary error). Shim CLI surface verified
byte-identical against pre-edit binaries across 16 scenarios each.
Step 4's open item is resolved: exec debug runs write log.txt again
through the same helper.

Step 8 landed 2026-08-18: `runinpopup/cli` owns the schema description
(`ConfigFieldDoc`/`ConfigDocs`/`ConfigSchemaHelp`) and the backend name
list (`BackendNameList` off `backend.Names()`); `run-in-popup config`
help renders from them (now names `tmux-floating-pane`; columns
machine-aligned). Reflection lives in tests only: mirror test between
`Config`/`PartialConfig` JSON trees, an Apply-reaches-every-field test
(closing the forgotten-merge-branch gap), env-tag match, docs/help
coverage, and Cobra-level guards — each verified to fail by injecting a
scaffold field. `Apply` stays hand-written (two branches + delegate;
codegen judged not worth it). Scaffold TODOs in root.go removed. Config
file schema/JSON bytes unchanged.

The README Library section was rewritten against the real surface
(example compile-checked) and the final gate ran green; see the State
block above for the follow-up list. No next action — the batch is
complete.

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
- [x] Step 5 — TTY rendezvous + Assuan transformer + iopipe; pinentry
      migrated (D2: "Split TTY acquisition from Assuan rewriting and pinentry
      process execution"; D3: "iopipe … rather than an ad hoc os.Pipe plus an
      unowned goroutine"; D13: "`PinentryLauncher`"; D12 applied to the
      pinentry exchange surface)
- [x] Step 6 — legacyshim + workspace ownership (D11: "extract an
      `internal/legacyshim` runner … each `main.go` becomes a tiny adapter";
      D1: shims no longer create their own run directories)
- [x] Step 7 — mechanical exec split (P2: "exported API and JSON are
      byte-for-byte unchanged")
- [x] Step 8 — config schema source + residue cleanup (P2: "one clear
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
