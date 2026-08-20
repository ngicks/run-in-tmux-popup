# STATUS — exec becomes a plain stream bridge

State: **done** — both steps landed 2026-08-19/20 and the verification
gate passed: full suite (`-race -count=2 ./...` included), hermeticity
stubs, both vet tag sets, lint, and all grep gates green; multi-focus
review verdict approve-with-nits with no blocking defects, and its four
minors (stale handshake-hardening prose, `WaitStreams` doc on
stream-less launches, the dismissal test's racy verdict documented,
this STATUS header) fixed in the closing commit. The same session also
landed four orthogonal user decisions recorded in DECISION.md:
`runworkspace.Open` always creates the run directory, the tmux
tty-handshake guard secrets are removed, `Config.DefaultBackend` is
renamed `Config.Backend` (schema break), and zellij popup env travels
via a sourced 0600 env file in the workspace instead of the argv.

Amendment landed 2026-08-20 (user decision, recorded in DECISION.md):
the bridge now takes all three stdio streams and the popup terminal
moves to fd 3/4/5 + TTY_IN/TTY_OUT/TTY_ERR, captured by the launch's
command-line prefix before the FIFO redirections; exec bridges stdin
(pipelines flow through the popup) and its input relay is excluded
from every wait so a caller terminal that never EOFs cannot hang the
bridge. Verified against a real pty during implementation; pinentry
and env-only launches get no prefix and are byte-identical.

No next action — the sub-plan is complete. Known open follow-up
elsewhere: the parent plan's follow-up list (parent STATUS.md), plus
exec's Ctrl-C error identity being racy (nil vs "popup failed:
signal: killed"), noted in the closing review as acceptable.

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
- [x] Verification — grep gate empty; bridge tests (byte delivery,
      dismissal, launch failure, rendezvous timeout); full suite +
      race + hermeticity green

## Open questions

| # | Question | State |
| --- | --- | --- |
| 1 | idea gate (bridge semantics, stdin on popup TTY) | resolved 2026-08-19 — confirmed |
| 2 | exec's exit code without the wrapper | resolved 2026-08-19 — exchange status only |
