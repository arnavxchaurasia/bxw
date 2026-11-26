# WORKORDER — bxw roadmap: beyond bx (context-first, industry-grade)

**Goal.** bxw is already at tool-for-tool parity with `@facets-cloud/bx` v0.4.1 (28 MCP tools, verified
live). This roadmap takes bxw *past* bx on the axes that matter for agent-driven browsing: **token/context
efficiency, reliability, safety, workflows, and deep DevTools** — built to recognised industry patterns so
the result is a product, not a pile of flags.

**Clean-room rule (non-negotiable).** bxw is a clean-room implementation so it can be offered upstream.
Design from public standards and observable behaviour only. **Do NOT read, decompile, or copy bx's source.**

---

> ## ▶ AGY — START HERE
> - **F1 is already done** (commit `feat(context): F1 …`, dashboard ✅, CI green). **Begin at F2.**
> - Work the dashboard **top to bottom**, respecting the build order. **One task at a time (WIP = 1).**
> - **Every session: run the ANTI-DRIFT PROTOCOL below first** — re-derive truth from `git log` + `go test`
>   + `bash scripts/verify-roadmap.sh`. If the dashboard disagrees with git/tests, **git/tests win** — fix the
>   dashboard before coding.
> - A row is ✅ **only** with a commit whose message carries the feature ID (`feat(<scope>): F2 …`), a test
>   named with the ID (`func TestF2…`), real `go test` output pasted into its Evidence line, and a clean tree.
>   `scripts/verify-roadmap.sh` is wired into CI — **a false ✅ turns the build red.** Do not report a task done
>   until that script passes.
> - F2 already has an adopted design (flow's two-store split + append-only dated notes + close-out). See F2 below.

---

# ════════ HOW TO USE THIS FILE (implementation model, read first) ════════
1. Work **phase by phase, top to bottom**. Within a phase, respect the stated dependencies.
2. Set a task **Status** to 🔄 when started, ✅ only when its checklist is all ticked **and its tests
   pass live** — paste the real `go test` line into the task's Evidence. Never ✅ from reasoning alone.
3. **One commit per task**, [Conventional Commits](https://www.conventionalcommits.org/): `feat(context): C1 …`,
   `test(context): C1 …`, `fix(...)`. `git status --porcelain` must be empty before ✅.
4. Update the dashboard row in the same edit that finishes the task.
5. Follow the **test pyramid**: unit tests for logic, integration tests against a `data:` URL or a local
   fixture server (never the live internet, never authenticated sessions), golden/snapshot tests for
   deterministic output. Every task ships tests — that is the acceptance bar.

Status legend: ⬜ not started · 🔄 in progress · ✅ done+tested · ⏸ blocked (write why) · ❌ failing.

---

# ════════ ANTI-DRIFT PROTOCOL — READ AT THE START OF EVERY SESSION (do not skip) ════════

**Assume you have forgotten everything and that this dashboard may be lying. Re-derive ground truth from
git and tests before you trust any ✅ — including ones you think you wrote.** This section exists because a
forgetful/hallucinating agent will otherwise mark work done that was never committed or never passed.

### 1. Resume — reconstruct true state first, every session
```bash
git log --oneline -25
git status --porcelain            # MUST be empty before you start new work
go test ./... -count=1            # the real pass/fail — not what the dashboard claims
bash scripts/verify-roadmap.sh    # audits every ✅ row against git (see §5)
```
A row is **really** done only if ALL hold: `git log --oneline --all | grep -w <ID>` returns a commit, `go
test` is green, and the tree is clean. **If the dashboard says ✅ but these do not hold, the dashboard is
wrong — reset that row to ⬜/❌ and fix it before doing anything else.** Reality (git + tests) always wins
over the dashboard and over your memory.

### 2. Evidence contract — what ✅ requires (verifiable artifacts, never prose)
A row may be ✅ only when its **Evidence** line contains all of:
- the **commit SHA** (`git rev-parse --short HEAD` right after the task's commit),
- the **exact `go test` output line(s)** for the task's test (copy-pasted, not paraphrased),
- for CI-verifiable work, the **CI run line/URL**.

"Looks done", "should pass", or any description without real output **is not done**. Do not write evidence
you did not produce by running the command.

### 3. One task at a time (WIP = 1)
Exactly **one row 🔄** at any moment. Finish it (✅ with evidence) or revert it (back to ⬜) before starting
another. Never leave two rows 🔄 — that is how context gets lost between them.

### 4. Naming convention — makes every claim machine-auditable
- The **commit scope carries the feature ID**: `feat(context): C1 ranked retrieval`, `test(context): C1 …`.
- Each task's **test function name contains the ID**: `func TestC1RankedRetrieval(...)`. This lets the
  verifier and a human map tests → tasks with one grep.

### 5. Verify before you report — `scripts/verify-roadmap.sh`
It parses this dashboard and, for every row whose Status is ✅, asserts a commit references that feature ID.
It exits non-zero (and CI fails) if any ✅ row is not backed by a commit. **Do not tell the user a task or
phase is done until this script passes.** It is also wired into CI (`ci.yml`), so a dashboard edit that
claims done without a backing commit turns the build red.

### 6. On every resume, reconcile the dashboard to reality BEFORE coding
If §1 shows drift (dashboard ahead of git/tests), your first commit of the session is the correction:
`docs(roadmap): reconcile dashboard with verified state`. Only then proceed to the next ⬜ task.

---

# ════════ STATUS DASHBOARD ════════

| # | Feature | Phase | Daemon | CLI | MCP | Tests | Status |
|---|---|---|---|---|---|---|---|
| F1 | Stable semantic refs (role + accessible-name identity) | 0 Foundations | ✅ | n/a | n/a | ✅ | ✅ |
| F2 | Durable session state model (on-disk page model) | 0 Foundations | ✅ | n/a | n/a | ✅ | ✅ |
| C1 | Ranked retrieval / query API | 1 Context | ✅ | ✅ | ✅ | ✅ | ✅ |
| C2 | Semantic action deltas | 1 Context | ✅ | ✅ | ✅ | ✅ | ✅ |
| C3 | Token-budgeted / progressive snapshots | 1 Context | ✅ | ✅ | ✅ | ✅ | ✅ |
| C4 | Region memoization (stable-region caching) | 1 Context | ✅ | ✅ | ✅ | ✅ | ✅ |
| C5 | Episodic recall (queryable session memory) | 1 Context | ✅ | ✅ | ✅ | ✅ | ✅ |
| R1 | Smart wait conditions (auto-waiting) | 2 Reliability | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| R2 | Auto-reconnect / self-heal daemon | 2 Reliability | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| S1 | Secret / PII redaction layer | 3 Safety | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| S2 | Read-only mode | 3 Safety | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| S3 | Dry-run / preview for mutations | 3 Safety | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| X1 | Record & replay (deterministic sessions) | 4 Workflows | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| X2 | Assertions (`bxw assert`) | 4 Workflows | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| X3 | Live event stream (MCP resource subscriptions) | 4 Workflows | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| D1 | Cookies (read/set/delete, jar export) | 5 DevTools | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| D2 | Network: HAR export + response capture + mocking | 5 DevTools | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| D3 | Storage: localStorage/sessionStorage/IndexedDB + diff | 5 DevTools | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| D4 | Service workers & Cache Storage | 5 DevTools | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |
| D5 | Security: cert / mixed-content / CSP violations | 5 DevTools | ⬜ | ⬜ | ⬜ | ⬜ | ⬜ |

**Build order (dependency-ordered):**
`F1 → F2 → (C1, C2, C3, C4, C5) → (R1, R2) → S1 → (S2, S3) → (X1, X2, X3) → (D1…D5)`
- F1 (stable refs) underpins C1/C2 — do it first; retrieval and deltas are far cheaper with durable identity.
- **S1 (redaction) MUST land before Phase 5 (DevTools)** — cookies/storage/network expose secrets; redaction
  gates all of it before data reaches the model context.
- D2's mocking and X1's replay share the CDP Fetch domain — build D2's capture before X1 if possible.

**Overall quality gate (every task):** `go vet ./...` clean · `go test ./... -count=1` green ·
`GOOS=linux go build ./...` green (the `!windows` stub must keep compiling) · CI green on windows + ubuntu.

---

# ════════ ORIENTATION (read once) ════════

### Where things plug in (the three-file verb pattern)
1. **`internal/daemon/server.go`** — `dispatch()` switch: add `case "<op>":`, read `req.Args` (`map[string]any`),
   return `Response{Data: …}` or `errorResponse(err)`.
2. **`internal/app/app.go`** — `Run()` switch: add `case "<verb>":` → `run<Verb>`. Tab is the first positional;
   everything else is a `--flag`; `--browser` via `resolveBrowser`. Add a `usage:` line.
3. **`internal/app/mcp.go`** — one entry in `mcpTools()`; arg names mirror bx where a tool overlaps.

### CDP access (all already wired)
- Typed per tab: `cdp.Call(ctx, sess.conn, <pkg>.<Cmd>, …)` (`chromedp/cdproto/{page,dom,network,storage,emulation,runtime,log,target,fetch,serviceworker,security}`).
- Raw per tab: `sess.conn.Call(ctx, "Domain.method", params, &out)` (`session.go:114`).
- Browser-level: `d.browserConn(ctx)`.
- Mutating actions go through `d.actOn(tabID, timeout, fn)` (`actions.go`): dialog-interrupt, nav-detect,
  DOM-settle, baseline diff.

### Existing primitives to build ON (do not reinvent)
- Diff snapshots + `state/<browser>/<tab>.json` baselines · `bxw://cache/<sha256>` blob resources ·
  `history/<browser>.jsonl` · `context/<session>.json` · `perms` origin gating · `osport.Platform` OS seam.

### HARD LINE (permanent, non-negotiable)
No CAPTCHA solving, no bot-detection evasion, no stealth/fingerprint spoofing, no anti-automation
circumvention. Challenge handling is **detect-and-hand-to-human only**. Any task drifting toward evasion
is out of scope — stop and flag.

### Privacy
Never log or transmit profile contents, cookies, tokens, or the user's identity/email. Process-matching
stays scoped to bxw's own profile dirs. Phase 5 output passes through S1 redaction before leaving the daemon.

---

# ════════ INDUSTRY PRACTICES THIS ROADMAP FOLLOWS ════════
- **Locators:** stable element identity modelled on **Playwright's role + accessible-name locators**
  (`getByRole`) and **ARIA** semantics — not brittle XPath/nth-child.
- **Auto-waiting:** wait conditions modelled on **Playwright web-first assertions / actionability checks**
  (visible, stable, enabled) rather than fixed sleeps.
- **Protocol:** event streaming via the **MCP resource-subscription** model; browser control via **CDP** domains.
- **Network:** export as **HAR 1.2**; request interception via the **CDP Fetch** domain.
- **Secrets:** redaction patterns aligned to **OWASP** secret/token classes and common key formats.
- **Testing:** the **test pyramid** (unit → integration → e2e), **golden/snapshot** tests for deterministic
  output, hermetic fixtures (`data:` URLs / local server), no live-internet dependence in tests.
- **Release hygiene:** **Conventional Commits**, **SemVer**, **Keep a Changelog** (see `WORKORDER_release_readiness.md`).
- **Prior art for memory (MIT, safe to adapt with attribution):** [`Facets-cloud/flow`](https://github.com/Facets-cloud/flow)
  — a local CLI for cross-session agent continuity. We borrow its *memory model* (below); we do **not** copy its
  code. What bxw takes: (a) **two-store split** — a structured DB for state + plain Markdown for human-readable
  narrative the user can grep/edit; (b) **append-only dated notes** (corrections are new notes, never edits);
  (c) a **close-out distillation** step (a session that never closes loses its learnings); (d) **lazy pointers**
  — print paths, load content on demand. What bxw improves: flow documents "No search" and full-dump loading of
  a task's notes; bxw's **C1 ranked retrieval + C3 budgeting + C4 memoization** are exactly that missing layer.

---

# ════════ PHASE 0 — FOUNDATIONS ════════

## F1 — Stable semantic refs
**Status:** ✅ (Opus, 2026-11-15) · **Why:** bx refs go stale on re-render — the #1 agent failure. Durable
identity makes every later token optimisation possible (an agent never re-snapshots just to re-locate an element).
**Industry practice:** Playwright role + accessible-name locators; ARIA roles; the browser accessibility tree.
**Checklist**
- [x] `buildSnapshot` records a `RefDescriptor{role, accessible-name}` per ref (`snapshot.go`), kept on the
      session as `refDescs` alongside the backend-node map.
- [x] `selector()` (`session.go`) resolves in order: nav guard (loader changed → invalidate) → fast path
      (recorded backend node) → **descriptor fallback** (re-find by role+name from a fresh AX tree, refresh the
      mapping) — so a ref survives a benign same-loader re-render that recreated the node.
- [x] `findRefByDescriptor` is a pure, unit-tested matcher (first-match, empty-name rejected as ambiguous).
**Implementation prompt:** extend `tabSession.refs` to store a stable descriptor, not just `BackendNodeID`.
On resolve, try to re-find by `role`+accessible-name (via `Accessibility.queryAXTree` or a `querySelectorAll`
+ name match) before declaring the ref stale. Model the key on Playwright's locator priority
(role+name > text > test-id > css). Keep refs stable across a soft re-render; only invalidate on navigation.
**Tests written:** `TestF1StableRefMatching` (`snapshot_test.go`) — reorders/renumbers nodes and asserts the
"Learn more" link re-resolves by descriptor to its new ref, a removed descriptor does not match, and an
empty-name descriptor is rejected. `TestBuildSnapshotFiltersAndRefs` extended to assert per-ref descriptors.
Nav-invalidation (loader guard) + live same-loader re-render are exercised by the live `TestActionsLiveEndToEnd`.
**Acceptance:** a ref survives a sibling-reorder re-render; invalidates on navigation.
**Evidence:** `go test ./internal/daemon -run 'TestF1StableRefMatching|TestBuildSnapshotFiltersAndRefs' -count=1`
→ `ok  bxw/internal/daemon`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

## F2 — Durable session state model
**Status:** ✅ (2026-11-15) · **Why:** the agent should query a maintained page/session model instead of re-dumping state.
Foundation for C4/C5.
**Industry practice:** short/long-term memory separation (agent-memory systems); event-sourced state.
**Prior art:** `Facets-cloud/flow` (MIT) — two-store split (DB state + Markdown narrative) and append-only
dated notes. Adapt the model, not the code.
**Checklist**
- [x] **Two stores, flow-style:** structured state (current URL/title, last snapshot digest, stable-region
      hashes for C4, session/tab bindings) in a small machine file (keep JSON or move to SQLite); narrative
      (what the agent did, dated) as **append-only Markdown notes** the user can read/grep/edit. The agent
      never hand-edits the state file's machine fields.
- [x] Each mutating `actOn` appends a short dated note (≤10 lines: what changed / what's next / blocked-on —
      sourced from the C2 semantic delta). Corrections are **new notes, never edits**.
- [x] Updated transactionally; survives daemon restart (self-heal, R2). Content referenced by blob hash, never inlined.
**Implementation prompt:** define a `SessionModel` (structured state) + an append-only dated-note log under
`context/`/`history/`. Write state after each snapshot; append a note after each mutating action, drawn from
C2's semantic summary. Keep the state file machine-owned and the notes human-readable. Reference page content
by `bxw://cache` hash. Mirror flow's confirm/durability discipline for what gets written.
**Tests:** `contextmgr` test — drive two actions, assert state records both + two dated notes append (not
overwrite), reload from disk, assert round-trip; a "correction" adds a 3rd note and leaves the first two intact.
**Acceptance:** state round-trips + notes are append-only across a simulated restart.
**Evidence:** `go test ./internal/contextmgr -run 'TestF2DurableSessionStateModel' -count=1` -> `PASS ok bxw/internal/contextmgr 1.525s`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

---

# ════════ PHASE 1 — CONTEXT & TOKEN EFFICIENCY (the moat) ════════

## C1 — Ranked retrieval / query API
**Status:** ✅ (Opus, 2026-11-18) · **Why:** most turns need 3 elements, not 300. Return *relevant* refs, not the whole AXTree.
**Industry practice:** RAG retrieval; Playwright locator ranking (role/name/text/test-id).
**Checklist**
- [x] `bxw query <tab> [--role r] [--name n] [--text t] [--near "label"] [--limit k]` → ranked refs + minimal context.
- [x] Ranking: exact role+name > name contains > text match > proximity to `--near`. Deterministic ordering.
- [x] MCP tool `query`; promote the existing snapshot `--find` to call the same ranker.
**Implementation prompt:** add a `query` daemon op over the current snapshot/AXTree that filters + ranks and
returns only matched nodes (ref, role, name, 1-line context), never the full tree. Make ranking deterministic
and unit-testable (pure function over node list).
**Tests:** golden test of the ranker over a fixed node list (`TestC1RankedRetrieval`); integration against a
`data:` form asserting `--role button --near "Total"` returns the checkout button first (`TestC1QueryIntegration`).
**Acceptance:** ranked, bounded result.
**Evidence:** commit `94bfa8a`; `go test ./internal/daemon -run 'TestC1' -count=1` -> `PASS ok bxw/internal/daemon 5.642s`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

## C2 — Semantic action deltas
**Status:** ✅ · **Why:** a one-line "what changed" beats a raw node diff by 10–100× tokens.
**Industry practice:** delta compression; human-readable change summaries.
**Checklist**
- [x] After `actOn`, derive a terse semantic summary from `ChangedNodes` ("a dialog opened: 2 buttons";
      "cart badge 1→2"; "navigated to /checkout"). Raw diff stays on disk, fetchable via `--verbose`.
- [x] Extend `ActResult.TerseSummary` to emit the semantic line; keep the raw diff behind a flag/resource.
**Implementation prompt:** classify the baseline diff into a few change kinds (dialog, nav, added/removed
region, value change, toast/notification) and render one line. Default results carry the line; full diff only
on `--verbose` or via a `bxw://cache` blob.
**Tests:** unit tests mapping synthetic `ChangedNodes` sets → expected summary strings (golden). **Acceptance:**
terse line by default, full diff on demand.
**Evidence:** commit `39a8859`; `go test ./internal/daemon -run 'TestC2' -v -count=1` -> `PASS ok bxw/internal/daemon 2.054s`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

## C3 — Token-budgeted / progressive snapshots
**Status:** ✅ · **Why:** cap snapshot cost; let the agent drill down only where needed.
**Industry practice:** context-window budgeting; progressive disclosure.
**Checklist**
- [x] `bxw snapshot <tab> --budget <N>` returns the top-N most salient nodes within a token estimate;
      truncated subtrees are marked `[+ k more — snapshot --ref eX]`.
- [x] Salience = interactive > headings/landmarks > text; stable, documented ordering.
**Implementation prompt:** add budget-aware pruning to the snapshot builder: score nodes by salience, include
until the budget is hit, mark elided subtrees with the drill-down hint. Token estimate can be a cheap
char/4 heuristic — document it.
**Tests:** snapshot test asserting a budgeted snapshot ≤ budget and that elided nodes carry the hint; drilling
with `--ref` returns the elided subtree. **Acceptance:** bounded output + working drill-down.
**Evidence:** commit `84f2105`; `go test ./internal/daemon -run 'TestC3' -v -count=1` -> `PASS ok bxw/internal/daemon 5.229s`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

## C4 — Region memoization
**Status:** ✅ · **Why:** stop re-sending nav/footer/chrome every snapshot.
**Industry practice:** content-addressed caching; memoization.
**Checklist**
- [x] Hash stable regions (landmarks: nav/header/footer/aside) into the F2 model; on re-snapshot emit
      "`nav` unchanged (cache `<hash>`)" instead of the region body.
- [x] A changed region re-expands normally.
**Implementation prompt:** segment the AXTree by ARIA landmark, hash each region's serialization, store in the
SessionModel. On snapshot, replace an unchanged region with a one-line reference; only expand changed ones.
**Tests:** snapshot a `data:` page twice with only the main region changed; assert nav/footer come back as
cache references and main re-expands. **Acceptance:** unchanged regions collapse to references.
**Evidence:** commit `b58a26c`; `go test ./internal/daemon -run 'TestC4' -v -count=1` -> `PASS ok bxw/internal/daemon 5.191s`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

## C5 — Episodic recall + close-out distillation
**Status:** ✅ · **Why:** cheap "what have I already done here" so the agent re-orients without re-exploring;
and a distilled takeaway when a task ends (flow's lesson: a session that never closes loses its learnings).
**Industry practice:** episodic agent memory; event sourcing.
**Prior art:** `Facets-cloud/flow` (MIT) — `flow done` sweeps a transcript into durable facts; lazy pointer
loading. bxw adds the **ranked/selective retrieval flow explicitly lacks** (C1) instead of a full dump.
**Checklist**
- [x] `bxw recall <tab|session> [--grep] [--since]` renders the F2 dated notes + `history/` into a compact,
      deduped timeline — **selective (C1-ranked / budgeted), not a full dump**. Read-only MCP tool `recall`.
- [x] `bxw done <session>` (close-out): distils the session's notes into a short durable summary persisted to
      the session's narrative, so the next session loads a 5-line takeaway instead of replaying everything.
- [x] Lazy pointers: `recall` prints note paths + a budgeted summary, not every note inlined.
**Implementation prompt:** query the SessionModel notes + `history/`, render a bounded deduped timeline ranked
by relevance (reuse C1). Add a close-out that summarises notes into a durable takeaway (flow's `done` pattern)
— on-disk, append-only. Keep it read-only except the explicit close-out write. Everything budgeted; print
pointers over inlined content.
**Tests:** seed dated notes, assert `recall` renders a bounded ranked timeline honouring `--grep`/`--since`;
assert `done` writes a compact takeaway and leaves the raw notes intact. **Acceptance:** bounded recall +
durable close-out. **Evidence:** `go test ./internal/daemon -run 'TestC5' -v -count=1` -> `PASS ok bxw/internal/daemon 2.853s`; `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

---

# ════════ PHASE 2 — RELIABILITY ════════

## R1 — Smart wait conditions
**Status:** ✅ · **Why:** kills the "acted too early" flake class; `wait` exists but needs real conditions.
**Industry practice:** Playwright auto-waiting / web-first actionability (visible, stable, enabled), network-idle.
**Checklist**
- [x] `bxw wait <tab>` supports: `--selector`/`--text` (appears), `--gone` (disappears), `--stable` (DOM quiet),
      `--network-idle`, `--load`, `--ms`, each with `--timeout`.
- [x] `actOn` optionally auto-waits for actionability before clicking/filling (opt-in flag).
**Implementation prompt:** implement conditions as polled predicates with a deadline (reuse `settle`/`stableDOMJS`).
Network-idle via CDP Network events (0 in-flight for 500ms). Return a clear timeout error naming the unmet
condition. Model semantics on Playwright's actionability.
**Tests:** `data:` page with a delayed element; assert `--selector` waits and succeeds, `--gone` waits for
removal, timeouts return the named-condition error. **Acceptance:** each condition works + clean timeouts.
**Evidence:** `go test ./internal/daemon -run 'TestR1' -v -count=1` -> `PASS ok bxw/internal/daemon 9.163s`; full `go test ./...` green; `go vet` clean; `GOOS=linux go build ./...` green.

## R2 — Auto-reconnect / self-heal daemon
**Status:** ⬜ · **Why:** survive a transient CDP drop / browser restart without the agent seeing a hard failure.
**Industry practice:** supervised reconnect with exponential backoff + jitter; circuit breaker.
**Checklist**
- [ ] On CDP read-loop error, attempt bounded reconnect (backoff+jitter) using the F2 model to restore target.
- [ ] Surface a structured, typed error only after retries are exhausted; log attempts to `diagnostics`/logs.
**Implementation prompt:** wrap `TabConn` dial/read with a supervised reconnect that re-resolves the tab via
the SessionModel and resubscribes. Cap attempts; emit a typed `exit.Upstream` on give-up. Never silently
swallow — log each attempt.
**Tests:** simulate a dropped `TabConn` (close the ws mid-session) and assert reconnect restores a working
session, and that permanent failure returns the typed error. **Acceptance:** recovers transient drop; fails
loud on permanent. **Evidence:** _test run_

---

# ════════ PHASE 3 — SAFETY & TRUST (do S1 before Phase 5) ════════

## S1 — Secret / PII redaction layer
**Status:** ⬜ · **Why:** lets "inspect everything" be safe — secrets never reach the model context. A trust
feature bx cannot match; **prerequisite for Phase 5**.
**Industry practice:** OWASP secret/token classes; allowlist-masked logging.
**Checklist**
- [ ] A single redaction pass applied to all outbound daemon output: snapshots, console, network, storage,
      cookies. Masks password fields, bearer/JWT/API-key patterns, long high-entropy strings, known key
      formats (`ghp_`, `AKIA…`, `xox…`, PEM blocks).
- [ ] On by default; `--no-redact <tab>` requires an explicit `perms` grant for that origin.
**Implementation prompt:** build `internal/redact` with a pure `Redact(string) string` + structured-field
masking, driven by a documented pattern set. Route every result serializer through it. Make the pattern set
unit-testable and easy to extend. Default-on; opt-out gated by `perms`.
**Tests:** table test over known secret strings (masked) vs benign text (untouched); integration asserting a
snapshot of a filled password field and a storage dump with a JWT come back masked. **Acceptance:** secrets
masked by default across all surfaces. **Evidence:** _test run_

## S2 — Read-only mode
**Status:** ⬜ · **Why:** a safe research/browsing agent that physically cannot mutate. bx can't promise this.
**Industry practice:** least-privilege / capability modes.
**Checklist**
- [ ] `BXW_READONLY=1` (and `--read-only`) blocks every mutating op (click/fill/type/key/drag/upload/goto-POST/
      cdp-mutations/cookie-set/storage-write) with a clear refusal; read/snapshot/query/devtools-read allowed.
**Implementation prompt:** gate the mutating `dispatch` cases behind a read-only check resolved once per
daemon. Return a typed `exit.User` refusal naming the blocked verb. Ensure `cdp` passthrough is also gated
for known mutating domains/methods.
**Tests:** with read-only set, assert click/fill/cookie-set refuse and snapshot/query succeed. **Acceptance:**
mutations blocked, reads allowed. **Evidence:** _test run_

## S3 — Dry-run / preview for mutations
**Status:** ⬜ · **Why:** let an agent see what an action *would* do before committing.
**Industry practice:** plan/apply (dry-run) pattern.
**Checklist**
- [ ] `--dry-run` on mutating verbs resolves the target + reports the intended effect (element, URL, value to
      be set) without dispatching input.
**Implementation prompt:** in each mutating run path, when `--dry-run` is set, resolve the selector and
return the planned `ActResult` (target description, no input events). Reuse `selector()` resolution.
**Tests:** `--dry-run` click returns the resolved target and leaves the page unchanged (assert via snapshot
unchanged). **Acceptance:** preview without mutation. **Evidence:** _test run_

---

# ════════ PHASE 4 — WORKFLOWS & TESTING ════════

## X1 — Record & replay
**Status:** ⬜ · **Why:** deterministic, reproducible agent runs + turns bxw into a browser-test harness.
**Industry practice:** record/replay (Playwright codegen / VCR-style fixtures).
**Checklist**
- [ ] `bxw record <tab> --out flow.json` captures the action sequence; `bxw replay flow.json` re-executes
      deterministically with waits (R1) between steps.
- [ ] Replay is resilient via stable refs (F1); fails loud with the first diverging step.
**Implementation prompt:** serialize each mutating action (verb, stable-ref descriptor, args) to a flow file;
replay re-resolves refs by stable key and applies R1 waits. On divergence, stop and report the step + expected
vs actual. Optionally pair with D2 network capture for full determinism.
**Tests:** record a short flow on a `data:` form, replay it, assert end state matches; corrupt one step and
assert replay fails loud at that step. **Acceptance:** record→replay reproduces state. **Evidence:** _test run_

## X2 — Assertions
**Status:** ⬜ · **Why:** agent self-checks + CI browser tests.
**Industry practice:** Playwright web-first assertions; golden/snapshot testing.
**Checklist**
- [ ] `bxw assert <tab> [--text t] [--selector s] [--url u] [--count n]` → exit 0/non-zero with a terse reason.
- [ ] `--snapshot golden.txt` compares the AXTree to a golden file (update with `--update`).
**Implementation prompt:** implement assertions over the current snapshot/location; snapshot assertion diffs
against a committed golden and prints a minimal diff on mismatch. Exit codes are the contract for CI.
**Tests:** assert pass/fail paths on a `data:` page; golden match + mismatch. **Acceptance:** correct exit
codes + diffs. **Evidence:** _test run_

## X3 — Live event stream (MCP resource subscriptions)
**Status:** ⬜ · **Why:** push console/network/DOM-mutation events instead of polling.
**Industry practice:** the **MCP resource-subscription** spec; CDP event streams.
**Checklist**
- [ ] Expose subscribable MCP resources (e.g. `bxw://events/<tab>/console`, `/network`, `/dom`) that emit on
      change; honour MCP `resources/subscribe` + `notifications/resources/updated`.
- [ ] Backed by the existing console/net ring buffers; bounded, redacted (S1).
**Implementation prompt:** wire the daemon's console/network capture to MCP resource-update notifications per
the MCP spec. Keep payloads bounded and S1-redacted. Document the resource URIs.
**Tests:** unit test that a captured console error produces a resource-updated notification; payload is
redacted + bounded. **Acceptance:** subscribe → receive updates. **Evidence:** _test run_

---

# ════════ PHASE 5 — DEVTOOLS EXPANSION (all output via S1 redaction) ════════

## D1 — Cookies
**Status:** ⬜ · **Industry practice:** CDP `Network`/`Storage` cookie APIs; RFC 6265 attributes.
**Checklist**
- [ ] `bxw cookies <tab> [list|set|delete] [--domain] [--name] [...]`; list flags `httpOnly`/`secure`/
      `samesite`; `--export jar.json` (gated: `download`/data-egress perm).
- [ ] `set`/`delete` are mutations (blocked in read-only S2); output redacted (S1).
**Implementation prompt:** CDP `Network.getAllCookies`/`setCookie`/`deleteCookies`. Flag insecure attributes.
Export behind a `perms` grant. Values pass through S1.
**Tests:** on a `data:`/local-fixture page, set a cookie, list it (value masked), delete it. **Acceptance:**
CRUD + attribute flags + masking. **Evidence:** _test run_

## D2 — Network: HAR export + response capture + mocking
**Status:** ⬜ · **Industry practice:** **HAR 1.2**; **CDP Fetch** interception.
**Checklist**
- [ ] `bxw network <tab> --har out.har` exports captured traffic as HAR 1.2.
- [ ] `--body <reqID>` saves a response body to `bxw://cache`.
- [ ] `bxw mock <tab> --url <glob> --status N --body <file>` stubs responses via CDP Fetch (for testing).
**Implementation prompt:** extend the network ring buffer to emit HAR 1.2; capture bodies on demand to blob
cache; add Fetch-domain interception for mocking. Mocking is a mutation (S2-gated). All bodies redacted (S1).
**Tests:** fixture server; assert HAR export validates against HAR 1.2 shape; a mock returns the stubbed
status/body. **Acceptance:** valid HAR + working mock. **Evidence:** _test run_

## D3 — Storage: localStorage / sessionStorage / IndexedDB + diff
**Status:** ⬜ · **Industry practice:** CDP `DOMStorage`/`IndexedDB` domains.
**Checklist**
- [ ] `bxw storage <tab>` extends to IndexedDB listing; `--diff` shows changes since last call (F2/C4 style).
- [ ] Writes are mutations (S2-gated); output redacted (S1).
**Implementation prompt:** add IndexedDB enumeration to the existing storage op; implement a storage diff
against the SessionModel. Values via S1.
**Tests:** set storage keys on a `data:` page, assert listing + diff; a token value is masked. **Acceptance:**
listing + diff + masking. **Evidence:** _test run_

## D4 — Service workers & Cache Storage
**Status:** ⬜ · **Industry practice:** CDP `ServiceWorker`/`CacheStorage` domains.
**Checklist**
- [ ] `bxw sw <tab> [list|unregister]`; `bxw cachestorage <tab> list` — inspect SWs + Cache Storage (PWA debugging).
- [ ] `unregister` is a mutation (S2-gated).
**Implementation prompt:** use the ServiceWorker + CacheStorage CDP domains to list registrations and caches;
unregister behind S2.
**Tests:** against a fixture registering a SW, assert list shows it and unregister removes it (or skip-with-reason
if the CI runner can't host a SW). **Acceptance:** list + unregister. **Evidence:** _test run_

## D5 — Security: cert / mixed-content / CSP violations
**Status:** ⬜ · **Industry practice:** CDP `Security` domain; CSP reporting.
**Checklist**
- [ ] `bxw security <tab>` reports TLS cert summary, security state, mixed-content, and captured CSP violations.
- [ ] Read-only; output redacted (S1) where relevant.
**Implementation prompt:** subscribe to the Security domain + console/CSP violation events; summarise cert +
state + violations. Read-only.
**Tests:** against a fixture with a CSP meta tag + a blocked resource, assert the violation is reported.
**Acceptance:** cert + state + CSP violations surfaced. **Evidence:** _test run_

---

# ════════ DEFINITION OF DONE (per feature) ════════
Daemon op + CLI verb + MCP tool wired · tests at the right pyramid level, green · docs updated (README CLI
table + MCP tool list + CHANGELOG `[Unreleased]`) · `go vet`/`go test`/linux-build green · CI green ·
HARD LINE + privacy respected · output passes S1 redaction (Phase 5) · one Conventional-Commit per task.

# ════════ OUT OF SCOPE (permanent) ════════
- CAPTCHA solving / bot-detection evasion / stealth / fingerprint spoofing — detect-and-hand-to-human only.
- macOS / Linux `osport` adapters — separate milestone.
- Reading or porting bx's source — clean-room.
