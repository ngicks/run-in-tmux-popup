# STATUS — exec becomes a plain stream bridge

State: **in progress** — scaffolded 2026-08-19; the initial "delete
exec entirely" direction was retracted by the user at the first gate
round and replaced by the stream-bridge shape (see DECISION.md "Exec
stays as a stream bridge"). Gate confirmed and the exit-code question
resolved (exchange status only) 2026-08-19.

Next action: step 1 (rewire exec onto PopupLauncher, delete payload
machinery).

## Checklist

- [x] Step 1 — rewire exec onto `PopupLauncher` + delete payload
      machinery ("wrapper deleted; popup runs the command directly";
      "stdout/stderr connected to the caller"; `ExecResult` JSON
      deleted; JsonIpc contract stated). Added
      `PopupCommand.WaitStreams()` so the streams decide the bridge's
      outcome — plain `Wait` would fail on tmux display-popup's
      status-carrying launcher, violating "exit code is the exchange's
      status" (see PLAN.md delta).
- [x] Step 2 — docs and help ("README/help carry no payload/JSON-result
      references"; contract stated in README Library section)
- [ ] Verification — grep gate empty; bridge tests (byte delivery,
      dismissal, launch failure, rendezvous timeout); full suite +
      race + hermeticity green

## Open questions

| # | Question | State |
| --- | --- | --- |
| 1 | idea gate (bridge semantics, stdin on popup TTY) | open |
| 2 | exec's exit code without the wrapper | open — default (a) exchange-status only |
