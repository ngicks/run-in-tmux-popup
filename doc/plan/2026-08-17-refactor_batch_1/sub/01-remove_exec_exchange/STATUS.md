# STATUS — exec becomes a plain stream bridge

State: **planned (idea gate pending)** — scaffolded 2026-08-19; the
initial "delete exec entirely" direction was retracted by the user at
the first gate round and replaced by the stream-bridge shape (see
DECISION.md "Exec stays as a stream bridge"). Plan rewritten
accordingly. Waiting on the idea gate and open question 2 (exit code).

Next action: pass the idea gate + resolve exit-code question, then
step 1.

## Checklist

- [ ] Step 1 — rewire exec onto `PopupLauncher` + delete payload
      machinery ("wrapper deleted; popup runs the command directly";
      "stdout/stderr connected to the caller"; `ExecResult` JSON
      deleted; JsonIpc contract stated)
- [ ] Step 2 — docs and help ("README/help carry no payload/JSON-result
      references"; contract stated in README Library section)
- [ ] Verification — grep gate empty; bridge tests (byte delivery,
      dismissal, launch failure, rendezvous timeout); full suite +
      race + hermeticity green

## Open questions

| # | Question | State |
| --- | --- | --- |
| 1 | idea gate (bridge semantics, stdin on popup TTY) | open |
| 2 | exec's exit code without the wrapper | open — default (a) exchange-status only |
