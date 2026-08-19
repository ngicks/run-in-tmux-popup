# PLAN — exec becomes a plain stream bridge

Sub-plan of [`../../PLAN.md`](../../PLAN.md) (refactor batch 1).
`run-in-popup exec` keeps its invocation but becomes a direct
`PopupLauncher` consumer: the popup runs the user's command itself, and
exec bridges its stdout/stderr to the caller. The `exec-payload`
wrapper, `ExecResult` JSON report, and exit-status forwarding are
deleted; the JSON exchange remains library-only (`JsonIpcLauncher`).

## Goal / success criteria

- `run-in-popup exec [flags] -- command [arg...]` runs the command in
  the popup with the popup TTY as its stdin, streaming its stdout and
  stderr live to the caller's stdout/stderr.
- No symbol, help text, README sentence, or doc comment refers to
  `ExecPayload`, `ExecResult`, the payload argv, or result JSON.
- `JsonIpcLauncher`'s stdout-is-JSON contract is stated in its doc
  comment and README's Library section.
- Pinentry path untouched; full suite + both vet tag sets +
  `-race` green; suite stays hermetic.

## Scope

The DECISION.md clauses "Exec stays as a stream bridge" and
"stdout-is-JSON is the library contract". The stdout→JSON helper stays
out of scope ("Delete now, maybe re-add later").

## Non-goals

- No `ExecResult`-shaped replacement, no exit-status forwarding
  machinery, no deprecation shims.
- No semantic change to `PopupLauncher`/`JsonIpcLauncher`/pinentry.
- No new flags; existing exec flags keep their meaning where they still
  apply.

## Context (verified 2026-08-19)

- To delete: `runinpopup/exec_types.go`, `exec_payload.go`,
  `exec_status.go` (+ their tests, `exec_caller_test.go`, the exec half
  of `runinpopup/main_test.go`'s TestMain);
  `cmd/run-in-popup/commands/exec-payload.go` (+test);
  `zz_exitcode.go`'s `exitCodeError` (+test) — it exists only to
  forward the payload's status (`zz_exitcode.go:5-13`);
  `runinpopup/cli/exec.go` `RenderExecResult` (+test).
- To rewrite: `cmd/run-in-popup/commands/exec.go` — keeps
  `resolveRuntime`, `runworkspace.Open` debug policy, and the popup
  spec construction, but drops the `JsonIpcLauncher[[]string,
  ExecResult]` wiring for `PopupLauncher.Exec` with
  `PopupStreams{Stdout, Stderr}` endpoints; `cmd/run-in-popup/main_test.go`
  fixtures riding on exec-payload.
- Constraint from the parent batch: the library closes every endpoint
  it was handed once its stream ends, and library code must never close
  process-global stdio — the command wraps `os.Stdout`/`os.Stderr` in
  no-op closers at the entry point (the one place allowed to name
  them).
- Survivors: `JsonIpcLauncher`/`JsonIpcConn` + `jsonipc_test.go`
  (generic stubs, no exec dependency), launch layer, pinentry, shims.

## Public surface delta

```go
// ---- removed from package runinpopup ----
//     type ExecResult struct{ ... }            // and its JSON wire format
//     type ExecOutcome struct{ Ran bool; Status int }
//     const ExecPayloadCommandName, ExecPayloadStartupTimeoutFlag
//     func ExecPayload(ctx, argv, ExecPayloadOptions) (ExecOutcome, error)
//     type ExecPayloadOptions struct{ ... }

// ---- removed from package runinpopup/cli ----
//     func RenderExecResult(w io.Writer, result runinpopup.ExecResult) error

// ---- removed CLI surface ----
//     run-in-popup exec-payload (hidden subcommand)
//     exec's JSON result on stdout; exec's "command's exit code rides
//     in the JSON, exec itself exits 0" contract

// ---- changed CLI surface ----
// run-in-popup exec [flags] -- command [arg...]
//     runs command in the popup (stdin = popup TTY) and streams its
//     stdout/stderr to exec's own stdout/stderr, live.
//     Exit code: see open question 2.
```

## Implementation steps

### Step 1 — rewire exec, delete the payload machinery

- Rewrite `runExec` onto `PopupLauncher.Exec` with
  `PopupStreams{Stdout, Stderr}` (no-close wrappers around the
  process stdio); wait via `PopupCommand.Wait`.
- Delete the payload/result surface listed in Context; trim TestMain;
  unwire `exec-payload` from root; settle exit-code behavior per the
  resolved open question.
- State the stdout-is-JSON contract on `JsonIpcLauncher`; sweep doc
  comments for payload/result wording.
- Port CLI exec tests to the bridge behavior (stdout/stderr bytes
  arrive; dismissal ends the streams; launch failure reported).

### Step 2 — docs and help

- README: rewrite the `run-in-popup exec` section (bridge semantics,
  no JSON), drop the exec-payload explanation and old exit-code
  contract, adjust the Library section's exec sentence to the
  JSON-contract statement; sweep for strays.
- Verify no Cobra help mentions the payload or JSON result.

After each step: `go test ./...`, `go vet ./...`,
`go vet -tags integration ./...`, `go test -race -count=1 ./runinpopup`.

## Testing and verification

- Grep gate: `grep -ri "execpayload\|execresult\|exec-payload\|RenderExecResult"`
  over the module (plan dirs excluded) is empty.
- Bridge tests cover: byte-accurate stdout and stderr delivery,
  interleaving-free per-stream ordering, popup dismissal mid-command,
  launch failure, rendezvous timeout (command never opens the FIFOs).
- Generic `jsonipc_test.go` coverage untouched.

## Risks

- Stream-end semantics differ by backend (display-popup launcher waits;
  floating-pane/zellij exit early) — `PopupCommand.Wait` already waits
  for endpoint streams; tests must pin that exec returns when the
  command's streams end, not when the launcher exits.
- Without the wrapper there is no reliable inner exit status; whatever
  open question 2 decides must be stated in help/README so users stop
  parsing JSON for it.

## Open questions

1. *(idea gate)* Confirm IDEA.md: exec = live stdout/stderr bridge,
   stdin stays on the popup TTY, no JSON, wrapper deleted.
2. Exit code of `run-in-popup exec`: the wrapper that smuggled the
   inner command's status out is gone, and only tmux display-popup's
   launcher carries the payload status (floating-pane/zellij launchers
   exit early). Options: (a) exchange-status only — 0 when the bridge
   worked, 1 on launch/exchange failure, inner status not reported
   (consistent across backends; default); (b) forward the status where
   the backend provides it (inconsistent across backends).

## Decision → step traceability

| Decision | Operative clause | Owning step |
| --- | --- | --- |
| Exec stays as a stream bridge | wrapper deleted; popup runs the command directly | 1 |
| Exec stays as a stream bridge | stdout/stderr connected to the caller ("つなぐだけ") | 1 |
| Exec stays as a stream bridge | `ExecResult` JSON deleted | 1, 2 (docs) |
| stdout-is-JSON is the library contract | stated on `JsonIpcLauncher` + README Library | 1, 2 |
| Delete now, maybe re-add later | helper NOT designed here (non-goal) | plan scope itself |
