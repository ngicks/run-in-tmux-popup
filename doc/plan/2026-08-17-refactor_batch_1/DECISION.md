# Decision log — refactor batch 1

Source document: [`../refacotr.md`](../refacotr.md), whose "Agreed direction"
section states its seven points are "decisions for the next design pass, not
open ideas". They are recorded here as already-resolved entries D1–D7 so the
plan's traceability gate can cite them. D8–D11 were the plan's open
questions; the user resolved all four on 2026-08-18. D12 is a planner-made
design call settling a field shape D1 left open.

## D1 — Extract the common popup-session lifecycle (resolved, 2026-08-17)

**Choice:** Add an unexported `popupSession`/`withPopupSession` layer under
`runinpopup` that owns: private work directory, FIFO-set creation/cleanup,
`Backend.Prepare`/restore, popup launch and launcher diagnostics, exchange
execution, and cleanup on every exit. "Backend capabilities may alter
construction and launcher behavior, but must not duplicate the lifecycle."

**Rationale:** `CallExec` and `CallPinentry` duplicate the whole lifecycle
today; workspace creation is additionally duplicated in `runExec`,
`runPinentry`, and both compatibility shims.

**Rejected:** A generic FIFO API hiding both exchange protocols (their
direction, completion, and error semantics differ); an exported hook-heavy
session framework (would freeze accidental complexity into the public API —
export only after a third real exchange needs it).

## D2 — Split TTY acquisition from Assuan rewriting and pinentry execution (resolved, 2026-08-17)

**Choice:** Three separate parts: a generalized TTY rendezvous capability
(`TTYHandshaker`/`TTYHandshake`, renamed from `PinentryHandshaker`), a small
Assuan stream transformer (`rewriteAssuanTTY`), and a pinentry coordinator
owning child-process execution.

**Rationale:** The backend capability is "open a popup, report its TTY, stay
alive until dismissed" — not inherently about pinentry; the Assuan rewrite is
a pure, testable transformation.

**Rejected:** A general Assuan framework (a focused transformer with
transcript tests is enough).

## D3 — Adopt `github.com/ngicks/go-common/iopipe` for cancellable stdio (resolved, 2026-08-17)

**Choice:** Create `iopipe` controllers at the OS-stdio boundary; the pinentry
coordinator consumes derived `io.ReadCloser`/`io.WriteCloser`. The library
never closes `os.Stdin`/`os.Stdout` itself; every `Pipe` completion channel is
consumed exactly once with the error-priority rules in `../refacotr.md`
("Adopt `github.com/ngicks/go-common/iopipe`" § Refactor).
*(Narrowed to the input stream only; see the amendment at the end of this
file.)*

**Rationale:** Replaces an ad hoc `os.Pipe` plus an intentionally-unjoined
`io.Copy` goroutine that closes process-global `os.Stdin` to unblock itself.

**Rejected:** Keeping the unowned-goroutine pattern.

## D4 — `runinpopup/backend` owns backend vocabulary (resolved, 2026-08-17)

**Choice:** Move canonical name constants, `Names`, and `DetectName`
implementations into `runinpopup/backend`. Parent `runinpopup` keeps only the
mechanism-neutral contract (`Backend`, `PopupSpec`) and session orchestration.

**Rationale:** `runinpopup/backend` is the factory package and natural owner;
its current constants only forward to the parent. Import direction (concrete
backends import the parent contract) makes parent-side aliases impossible.

**Rejected:** Keeping forwarding constants in both packages (defeats the
ownership fix). Whether the parent's old exported symbols get a compatibility
period is D10.

## D5 — Extract CLI runtime/backend resolution (resolved, 2026-08-17)

**Choice:** Proceed with the P1 extraction: a command-layer
`resolveRuntime(inputs, environ)` assembler in
`cmd/run-in-popup/commands/runtime.go`; environment policy stays in the CLI,
not in the `runinpopup` library.

**Rationale:** `runExec` and `runPinentry` duplicate the six-step
resolve/construct sequence and their policy comments have already diverged.

**Rejected:** Hiding `os.Getenv` inside the library service.

## D6 — Multiplexer invocation moves to `runinpopup/internal/tmux` and `runinpopup/internal/zellij` (resolved, 2026-08-17)

**Choice:** Internal executable clients own argv, environment,
`exec.CommandContext`, SIGINT cancellation, stderr decoration, and raw-output
parsing. Public backends keep mechanism policy and delegate. Internal clients
do not import `runinpopup.PopupSpec`; they define their own request types.
Shell-line rendering moves alongside; only POSIX word quoting is shared via
`runinpopup/internal/shellargv` if both need it.

**Rationale:** `startPopup` currently leaks executable mechanics into the
shared session layer, and `tmux_floating_pane.go` duplicates invocation
handling for version/zoom queries.

**Rejected:** A dynamic backend registry (three compile-time backends do not
justify it).

## D7 — Remove `Backend.PopupCommand` and `Backend.Environ` (resolved, 2026-08-17)

**Choice:** Replace with active launch delegation:
`Launch(ctx, spec, streams) (PopupLauncher, error)` where `PopupLauncher` has
only `Wait() error`; cancellation is part of `Launch` via the internal
client's SIGINT-on-context-end setup. Raw argv and process environment leave
the public backend contract entirely.

**Rationale:** Retaining the old methods as optional/compat would preserve two
competing invocation paths.

**Rejected:** Keeping `PopupCommand`/`Environ` as deprecated methods.

## D8 — Plan structure: single plan (resolved by user, 2026-08-18)

**Choice:** Keep this as a single plan with eight ordered steps; no master /
sub-plan split.

**Rationale:** The slices are already ordered, independently verifiable steps
with one shared success criterion (behavior preservation); sub-plan boundary
ledgers would add coordination overhead without isolating any real ownership
boundary.

**Rejected:** A master plan with one sub-plan per slice under `sub/NN-*/`.

## D9 — Remove exported `runinpopup.Run` outright (resolved by user, 2026-08-18)

**Choice:** Delete `Run` and `RunOptions` (`runinpopup/run.go:16,37`) in the
same pre-1.0 break as D7/D10. Only the high-level protocol-backed operations
(`CallExec`, `CallPinentry`) remain exported; low-level launching is covered
by the internal `popupLaunch`/`launchPopup` handle from step 4.

**Rationale:** `Run` has no callers outside its own tests
(`runinpopup/run_test.go:47-101`), and its launcher-exit completion means
observably different things per backend (tmux `display-popup` waits for popup
closure; zellij/floating-pane launchers may return right after pane
creation). Removing it eliminates the misleading primitive instead of
documenting around it.

**Rejected:** Deprecating it (keeps the misleading semantics alive);
redefining it with payload-completion semantics (a behavior change to an
exported function); adding an exported low-level `Launch` replacement now —
judgment call by the planner following the source doc's "export only after a
real external need" stance: the internal handle suffices, revisit if an
importer asks.

## D10 — Backend vocabulary API break: clean break (resolved by user, 2026-08-18)

**Choice:** Remove `runinpopup.BackendTmuxPopup` / `BackendTmuxFloatingPane` /
`BackendZellij` / `BackendNames` / `DetectBackendName` in one release, with no
compatibility period. The break is called out in release notes together with
the D7 and D9 breaks.

**Rationale:** The module is pre-1.0, and duplicated deprecated constants
would defeat the single-ownership fix D4 exists for.

**Rejected:** A compatibility period with duplicated deprecated
constants/functions forwarding from `runinpopup` to `runinpopup/backend`.

## D11 — Deprecated binaries survive via `internal/legacyshim` (resolved by user, 2026-08-18)

**Choice:** `cmd/tmux-popup-pinentry-curses` and
`cmd/zellij-popup-pinentry-curses` are kept for multiple releases. Extract an
`internal/legacyshim` runner parameterized by backend kind, validation text,
workspace prefix, and debug compatibility; each `main.go` becomes a tiny
adapter. No removal release is scheduled in this batch; their CLI surface is
unchanged.

**Rationale:** Product decision by the user: deleting installed entrypoints
is a public-surface change they are not ready to schedule, and surviving
binaries justify centralizing the duplicated startup policy (signals,
deprecation output, user-data validation, backend construction, workspace,
logging).

**Rejected:** Announcing a removal release and adding only smoke tests;
removing both binaries in this batch.

## D12 — `WorkspaceOptions` replaces `TempDir`; retained path reported via log (resolved by planner, 2026-08-18)

Routine design call by the planner, settling the field shape D1's "TempDir
becomes optional or is replaced by a common workspace option" left open;
raise it back if the shape is wrong.

**Choice:** Replace the required `ExecOptions.TempDir` / `PinentryOptions.TempDir`
with a shared `Workspace WorkspaceOptions` field: `Dir` (caller-owned, never
removed by the session; empty means the session creates a mode-0700 directory
under `os.TempDir()` and removes it on completion), `NamePrefix` (name prefix
for session-created directories, default `"run-in-popup-"`), and `Retain`
(keep a session-created directory; the retained path is reported through
`Logger`). Exact block in PLAN.md's **Public surface delta**.

**Rationale:** One struct expresses the D1 lifecycle (session owns the IPC
directory by default) for both exchanges symmetrically. Reporting the
retained path through the structured log keeps `CallPinentry` (which returns
only `error`) and `CallExec` uniform, and leaves `ExecResult` and its JSON
encoding byte-for-byte untouched — a hard success criterion.

**Rejected:** Keeping the `TempDir` name with optional semantics (a silent
meaning change on an unchanged field name is worse than a visible pre-1.0
break already bundled with D7/D9/D10); reporting the retained path through a
new `ExecResult` field (asymmetric with pinentry, and risks the frozen JSON
surface).

## D13 — Exported launch + exchange layering: `PopupLauncher` → `PopupCommand`; `PinentryLauncher`, `JsonIpcLauncher[T]` (resolved by user, 2026-08-18)

**Choice:** The library gets one wrapping entry-point struct for launching
popups — "piping those streams are backend-agnostic. We need one wrapping
entry point struct for popup functions. Pinentry and Exec are one additional
wrapper layer" (user, this session):

- `PopupLauncher` (exported struct): owns `Backend.Prepare`/restore,
  `Backend.Launch`, and stream piping; `Exec(ctx, spec)` launches and
  returns `*PopupCommand`.
- `PopupCommand` (exported, wrapper-owned): `Wait() error`,
  `Stdout() io.ReadCloser`, `Stderr() io.ReadCloser` — an un-wired stream
  returns an already-closed reader. Implemented once, backend-agnostically,
  which is why these methods are acceptable here but were rejected on the
  backend-returned handle. *(Superseded by D15's final stream model: the
  accessors were later removed; `PopupCommand` is wait-only.)*
- `Backend.Launch` returns a wait-only handle named `PopupHandle` (the
  planner's `BackendCommand` was vetoed by the user in the naming round) and
  keeps taking `PopupStreams` writers, which the wrapper wires to its pipes.
- The exchanges become one wrapper layer above: `PinentryLauncher` and the
  generic `JsonIpcLauncher[T any]` (exec = `JsonIpcLauncher[ExecResult]`).

**Supersedes:** D7's `PopupLauncher` interface name (that name now belongs
to the entry struct; the handle is `BackendCommand`); D9's "no exported
replacement" clause (`PopupLauncher.Exec` + `PopupCommand.Wait` are the
accurately named launch API — `Run`'s removal itself stands); D1's "export
only after a third real exchange needs it" stance for the launch layer (the
session internals behind the exchange launchers may still stay unexported).

**Rationale:** Stream piping is identical for every backend, so it belongs
in one wrapper, not in each backend; a wrapper-owned command handle gives
callers uniform stream access with no nil checks.

**Rejected (by the user, this session):** `Stdout()`/`Stderr()` methods on
the backend-returned handle; folding streams into `PopupSpec` as
`Stdin io.ReadCloser` / `Stdout, Stderr io.WriteCloser`; a single `Popup`
entry struct carrying the exchange operations as methods; keeping
`CallExec`/`CallPinentry` as free functions taking `Backend` or the entry
struct (the exchanges are structs instead; see D14).

## D14 — `CallExec`/`CallPinentry` and their options structs dissolve (resolved by user, 2026-08-18)

**Choice:** `ExecOptions`, `PinentryOptions`, `CallExec`, and `CallPinentry`
are removed; the D13 launcher structs are the only exchange API and the CLI
constructs them directly. Their fields distribute per D13/D15: shared
session infrastructure (`Workspace`, `StartupTimeout`, `Logger`) onto
`PopupLauncher` — superseding D12's attachment point (its `WorkspaceOptions`
shape and log-reported retention stand) — and exchange-specific fields onto
`PinentryLauncher`/`JsonIpcLauncher`.

**Rationale:** Keeping the functions would preserve two ways to run each
exchange; pre-1.0, one entry per operation is worth the break.

**Rejected:** Thin convenience wrappers delegating to the launchers.

## D15 — `JsonIpcLauncher` is bidirectional streaming JSON IPC; session infrastructure folds into `PopupLauncher` (resolved by user, 2026-08-18; signatures pending)

**Choice (user's sketch, verbatim):** "add field PartialSpec PopupSpec and
AddPlayload func(T, PopupSpec) PopupSpec. Don't make timeout and workspace
option for that. They should be folded into PopupLauncher. Also there should
be send T over stdin. Also, output must be stream of T, so <-chan T?"
Interpreted as: `JsonIpcLauncher[T]` carries `PartialSpec PopupSpec` plus an
`AddPayload` completion hook instead of prebuilt exec argv fields;
`Workspace` and `StartupTimeout` live on `PopupLauncher`, not per exchange;
values can be sent to the payload (arriving JSON-encoded on the payload's
stdin, wired via an input FIFO); results stream back as `<-chan T`. Exec
becomes `JsonIpcLauncher[ExecResult]` receiving one value and sending none;
the exec JSON bytes and `ExecPayload` argv convention are unchanged.

**Signature round (resolved by user, 2026-08-18):** two type parameters
`[In, Out any]`; `Call` returns a connection handle
(`Send`/`Results() <-chan Out`/`Wait`); `AddPayload`'s first parameter is
literally the input value. The user's underlying model, verbatim: "As per
PopupLauncher or Backend logic, fifo files are allcoated for
stdin/stdout/stderr and they'll be connected to streams passed and piped
reader for stdout/stderr is returned from launched command. Input may be
(1) sent over stdin or (2) treated as input arguments to command. Result is
returned from stdout. The result is assumed stream: 1 or multiple JSON
value is ok to be printed to stdout. AddPayload is for path (2). We'll need
to know what exact place to insert marshaled value. We could do it by let
users define index number or a function which returns index number. Or
maybe let callers configure constructor for Spec. At this point I took
latter idea." So the launch layer allocates the payload's stdio FIFOs and
`PopupCommand` exposes the payload's streams; there is no separate result
FIFO — results are the payload's stdout. Consequently the `ExecPayload`
argv convention changes (the result-FIFO path argument disappears; see the
delta block) while the result JSON bytes themselves stay identical — an
earlier draft of this entry wrongly said the argv convention was unchanged.

**Second signature round (resolved by user, 2026-08-18):** the stream fold
from the user's first message stands — payload stdio endpoints are fields
in the launcher-level `PopupSpec` (`Stdin io.ReadCloser`,
`Stdout, Stderr io.WriteCloser`; nil leaves that stream on the popup TTY,
per-`Exec` choice); the spec splits into the launcher-level `PopupSpec` and
a backend-level spec whose command line already carries the FIFO wiring
("Split PopupLauncher's Spec and Backend's spec. PopupLauncher can have
stream on each Exec"); `JsonIpcLauncher` has two entry methods — "Exec that
takes In and Start that returns stream controller" — replacing the single
`Call`.

**Third signature round (resolved by user, 2026-08-18):** `PopupCommand`
keeps `Stdout()`/`Stderr() io.ReadCloser` accessors with the rule: nil
endpoint → the FIFO's piped reader is live on the accessor; non-nil
endpoint → the endpoint consumes the stream and the accessor returns an
already-closed reader (so payload stdout/stderr are always FIFO-captured;
the exec payload hands the popup TTY to its inner command itself). The
backend-level spec is named `LaunchSpec`.
`JsonIpcLauncher.Exec(ctx, v In) (*JsonIpcConn[In, Out], error)` is the
single entry method — a separate `Start` was rejected "Because AddPayload
can decide add or ignore it".

**Stream-placement correction (user, 2026-08-18):** the planner had put the
payload stdio endpoints on `PopupSpec`; wrong — "Streams should be folded
into LaunchSpec. PopupSpec should not have it, because Backend's LaunchSpec
is built only for one-shot command. Contrarily, PopupSpec is persistent
template so streams should be excluded from it, So PopupStreams must be
separate struct." Final placement: `PopupSpec` is a stream-free reusable
template; `LaunchSpec` (one-shot) folds in the per-launch streams,
including the launcher-process diagnostic writers that were previously
`Backend.Launch`'s separate `streams` parameter (parameter removed —
planner call following the fold); the payload endpoints form the separate
`PopupStreams{Stdin io.ReadCloser; Stdout, Stderr io.WriteCloser}` passed
per `PopupLauncher.Exec(ctx, spec, streams)`.

**Uniform allocation rule (user, 2026-08-18):** "I'm changing interface to
allow users to control allocate tty for each stdin/stdout/stderr. Nil
stream = no allocation. Stdin is not an exception." So `LaunchSpec` carries
all three payload endpoints (stdin included), each independently
controlling whether a FIFO is allocated for that stdio; nil leaves it on
the popup TTY. This supersedes the planner's "stdout/stderr always
FIFO-captured" reading and its diagnostics-writer fields on `LaunchSpec`
(launcher-process output is internal logging instead), lets pinentry pass
all-nil streams, and reopened the `PopupCommand` accessor rule as open
question 14.

**Pipe methods, final form (user, 2026-08-18):** question 14 went through
a `Piped` sentinel and a wait-only interlude before the user named what
was missing — explicit os/exec-style pipe requests: "Rename Stdout and
Stderr to StdoutPipe()/StderrPipe(). They both return (io.ReadCloser,
bool) and latter bool is false if piping was not requested. PopupStreams
now have StdoutPipe bool and StderrPipe bool, if true then Launch
allocates os.Pipe. If Stdout or Stderr is not nil, they will have higher
priority." Final rules: `PopupStreams.StdoutPipe`/`StderrPipe` bools
request a piped reader exposed by `PopupCommand.StdoutPipe()`/`StderrPipe()
(io.ReadCloser, bool)`; bool false when piping was not requested; a
non-nil `Stdout`/`Stderr` endpoint overrides its bool; nil endpoint with
false bool = no allocation. The `Piped` sentinel is rejected — replaced by
the explicit bools + renamed methods.

**Pipe backing (resolved with user, 2026-08-18):** the reader behind
`StdoutPipe()`/`StderrPipe()` is an io.Pipe (or iopipe derived endpoint,
D3) fed by a pump goroutine off the FIFO — not os.Pipe (the user's sketch
mentioned os.Pipe before this was examined) and not the raw FIFO fd.
Rationale: a named FIFO is itself a kernel pipe with the same 64KiB
buffer, so an os.Pipe stage adds no meaningful capacity (`F_SETPIPE_SZ`
on the FIFO is the lever if one is ever needed); io.Pipe adds no fds and
its `CloseWithError` delivers the real terminal cause into a blocked
`Read`, matching D3's error-priority rules; the pump normalizes FIFO
open/EOF quirks (EOF-before-writer, blocking open) that returning the raw
FIFO would leak to callers. A stalled consumer backpressures the popup
command — accepted as the same semantics as `os/exec.StdoutPipe`.
Rejected: raw FIFO fd return; os.Pipe stage; unbounded user-space
buffering to shield the popup command.

**Rejected:** Exec argv fields (`Command`/`Title`/`PayloadPath`) on the
launcher; a `Spec func(resultFIFO string) PopupSpec` callback without a
partial-spec field; per-exchange workspace/timeout options; an endpoints
struct as `AddPayload`'s first parameter (FIFO wiring is launch-layer
logic, not the constructor's job); insertion-index schemes for placing the
marshaled input.

## Handshake rename inconsistency (resolved with user, 2026-08-18)

Step 5's instruction to rename `PinentryHandshaker`/`PinentryHandshake`
to `TTYHandshaker`/`TTYHandshake` conflicts with the "Public surface
delta" block, which declares itself exhaustive but omits the rename.
The user picked the rename: the exchange is a TTY rendezvous, not
pinentry-specific, matching step 5's "backends name no pinentry-specific
capability" gate. The rename joins the pre-1.0 break set; the delta
block's omission is treated as an oversight, not a veto.

## Amended: only the pinentry input is relayed; both outputs go back to the child (2026-08-22; narrows D3)

D3 put a controller in front of all three of the proxy's own standard
streams, and the pinentry child was given the derived ends. For the
outputs that was wrong, and it kept a popup on screen after the user had
already answered the prompt.

What went wrong: with a relay in the middle, the process writing to the
proxy's fd 1 and fd 2 was the proxy itself, copying what pinentry had
written. gpg-agent's side of the connection says BYE and then drops both
descriptors without waiting to be acknowledged, so the bytes pinentry
writes after that land on a pipe with no reader. A broken pipe on fd 1 or
fd 2 raises SIGPIPE, which is fatal by default — and it killed the proxy,
the one process that still had a popup to dismiss and a workspace to
clean up. The popup stayed until something else closed it.

The amendment: the two output endpoints are handed to the pinentry child
as they are, so os/exec passes the descriptors straight through whenever
they are files, which they are whenever gpg-agent is on the other end.
The child then answers on the very descriptor gpg-agent is reading, and a
hang-up breaks the pipe under the pinentry that gpg-agent stopped
listening to — a death the proxy is still alive to notice, report, and
dismiss the popup after. Nothing in the proxy stands between the two, so
the Assuan bytes are also no longer copied at all.

The input keeps its controller, unchanged and for the reason D3 gave: the
relay sits in a read on `os.Stdin` that only gpg-agent can end, so the
exchange needs an end of its own it is allowed to close. No SIGPIPE
concern arises there — nothing writes to fd 0.

Consequences: `processStdio` becomes `assuanInput` (one derived end, one
completion channel, no errgroup); `PinentryLauncher`'s unexported
`stdout`/`stderr` seams are passed to the child rather than wrapped; and
the iopipe writer quirk recorded alongside the old output relays is gone
with them. Live coverage came with the change: `pinentry_live_test.go`,
behind the `integration` build tag, drives a real pinentry-curses in a
real tmux popup on a private server and asserts that both the popup and
the proxy are gone shortly after the exchange ends — including the case
where gpg-agent drops the connection right after BYE, which is the one
that used to leave the popup behind.
