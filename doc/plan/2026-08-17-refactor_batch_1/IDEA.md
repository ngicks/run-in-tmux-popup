# IDEA — refactor batch 1

Gate: confirmed by user, 2026-08-17; exception list below aligned 2026-08-18
to the user's D8–D11 and D13–D15 answers of that day (see DECISION.md) — no
new idea content beyond transcribing those answers, so the confirmation
stands.

The user instructed skipping the IDEA phase for this plan ("Skip IDEA phase").
This is a behavior-preserving refactor batch: the CLI, JSON result format,
configuration schema, `PINENTRY_USER_DATA` contract, and backend behavior as
observed by end users must not change, except where an item in
[`../refacotr.md`](../refacotr.md) explicitly decides otherwise — as resolved
in DECISION.md: the pre-1.0 `Backend` interface break (D7), the
backend-vocabulary move with clean break (D4/D10), the removal of
`runinpopup.Run` (D9), and the user's launch/exchange-layer relayering
(D13–D15: `PopupLauncher`/`PopupCommand`/`LaunchSpec`,
`PinentryLauncher`/`JsonIpcLauncher` replacing `CallExec`/`CallPinentry`
and their options). The deprecated compatibility binaries are retained
(D11), so they are no exception: their surface stays unchanged. End-user
CLI behavior, JSON bytes, config schema, and `PINENTRY_USER_DATA` handling
remain outside every exception.

There are no new use cases; the "how it should be" statement is: everything a
user or importer can observe today keeps working the same way, delivered by a
codebase whose ownership boundaries match the decisions recorded in
DECISION.md.
