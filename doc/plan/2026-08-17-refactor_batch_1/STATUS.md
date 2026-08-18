# STATUS — refactor batch 1

State: **in progress** — step 1 landed 2026-08-18: six hermetic
characterization tests for `callPinentry` (fake handshaking backend +
fake pinentry via the test binary; three unexported stdio seams on
`PinentryOptions`), live tmux tests moved behind `//go:build integration`,
golden table tests for `cli.RenderConfig`/`cli.RenderExecResult`.
Hermeticity verified by running the default suite with unusable
tmux/zellij stubs first in PATH; `go vet` clean with and without the
`integration` tag.

Finding recorded during step 1 (pre-existing, deliberately not fixed):
`go test -race` fails on the new pinentry tests — `callPinentry` reads
the popup launcher's stdout/stderr buffers while os/exec copier
goroutines may still write (no `Wait` on the launcher, so no
happens-before edge); the launcher `exec.Cmd` is also never waited on
(goroutine/fd/zombie leak per call). Both are owned by the step 4–5
restructure of the launch/pinentry flow.

Next action: step 2 (internal tmux/zellij clients, backend vocabulary
move, `Launch`/`PopupHandle` contract break, `PopupLauncher`/`PopupCommand`
launch layer, delete `Run`/`RunOptions`).

## Checklist

- [x] Step 1 — hermetic test split + pinentry characterization (P0: "An
      Assuan transcript test proves the exact bytes forwarded to pinentry")
- [ ] Step 2 — internal clients + vocabulary + contract break (D6: "delegate
      executable operations to those internal clients"; D4: "own backend
      names, enumeration, detection, and construction"; D7: "raw argv and
      process environment are no longer part of the public backend contract";
      D10: "no compatibility period" — old parent-package symbols deleted;
      D9: `Run`/`RunOptions` deleted; D13/D15: `PopupLauncher.Exec(ctx,
      spec, streams)` returns `*PopupCommand` with `Wait` +
      `StdoutPipe`/`StderrPipe`; launcher-owned pipe goroutines connect
      FIFOs to the passed endpoints)
- [ ] Step 3 — CLI runtime resolver (D5: "Proceed with the P1 CLI
      runtime/backend-resolution extraction")
- [ ] Step 4 — popup session; exec migrated onto `JsonIpcLauncher` with
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
