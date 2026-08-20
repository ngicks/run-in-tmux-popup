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

Amendment landed 2026-08-20, then corrected the same day (both user
decisions, recorded in DECISION.md — the second supersedes the first):
the first cut bridged all three stdio streams and re-exposed the pts
on fd 3/4/5, which made `exec -- htop` draw on the calling terminal.
The corrected shape ships as `PopupStreams.KeepStdio`: the command
keeps the popup's pts on fd 0/1/2 (any TUI just works in the popup),
and the caller bridge attaches beside it — fd 3 reads the caller's
stdin, fd 4/5 write to its stdout/stderr, TTY_IN/TTY_OUT/TTY_ERR carry
the FIFO paths (dup with `<&3`, not a `/dev/fd/3` re-open, which
blocks once the relay closed — verified in a real shell). The $(tty)
prefix is retired; JsonIpc/pinentry keep redirect semantics; the
stdin relay stays outside every wait. Pipe workflows opt in on the
command side (`fzf <&3 >&4`).

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
