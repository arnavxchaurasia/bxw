# WORK ORDER — bxw remaining phases (execution plan)

**What this is:** the build-order / sequencing layer for everything planned in bxw but **not yet built** — the
18 features across Phases 1–5. Full per-feature specs (checklist, implementation prompt, tests, acceptance)
live in [`WORKORDER_roadmap_beyond_bx.md`](WORKORDER_roadmap_beyond_bx.md); **this file is the order to build
them in and the dependencies between them.** Do not re-spec here — follow the roadmap section each item links to.

**Done already (do not rebuild):** F1 stable semantic refs ✅, F2 durable session state model ✅ (Phase 0).
**C1 ranked retrieval / query API ✅** (Phase 1 #1) — commit `fc79f60`, verified: CI green (ubuntu + windows),
`verify-roadmap` OK, real headless-browser integration test. **Phases 1–2 (C1–C5, R1–R2) complete ✅ — next up: S1 (Phase 3, the redaction gate).**

## Rules (same as the roadmap — enforced)

- **One task in flight (WIP = 1).** A row is ✅ only with: a Conventional Commit whose message carries the
  feature ID (`feat(context): C1 …`), the real `go test` line pasted as Evidence, `go vet`/linux-build green,
  and a clean tree. `scripts/verify-roadmap.sh` fails CI on any ✅ not backed by such a commit — never mark
  done from reasoning.
- **Definition of done (per feature):** daemon op + CLI verb + MCP tool wired · tests at the right pyramid
  level, green · docs updated (README CLI table + MCP tool list + CHANGELOG `[Unreleased]`) · HARD LINE +
  privacy respected · Phase-5 output passes S1 redaction · one commit per task.
- **HARD LINE (permanent):** no CAPTCHA solving / bot-detection evasion / stealth / fingerprint spoofing —
  detect-and-hand-to-human only. No reading or porting bx's source (clean-room). macOS/Linux `osport` adapters
  are a separate milestone.

## Dependency graph (why the order matters)

```
F1 ✅ ─────────────► X1 (record/replay re-resolves by stable ref)
F2 ✅ ─────────────► C4 (region hashes live in SessionModel), C5 (notes), D3 (storage diff)
C1 ─┬─► C3 (budget uses salience/ranker)
    ├─► C5 (recall is C1-ranked, not a dump)
    └─► snapshot --find (reuse ranker)
R1 ─────────────► X1 (waits between replay steps)
S1 ★ ───────────► D1 D2 D3 D4 D5 (all DevTools output redacted) + X3 (event payloads redacted)
S2 ─────────────► D1 D2 D3 D4 (mutations blocked in read-only)
```
★ **S1 (redaction) is the gate for all of Phase 5.** It must land before any DevTools feature ships. It is
also bxw's strongest trust differentiator over bx — consider pulling it forward if you want that story sooner.

## Recommended build order (critical path)

1. ✅ **C1** — ranked retrieval — **DONE** (`fc79f60`, CI green, verify-roadmap OK)
2. ✅ **C2 · C3 · C4** — context/token-efficiency — **DONE**
3. ✅ **C5** — episodic recall + close-out — **DONE** (Phase 1 complete)
4. ✅ **R1** — smart wait conditions — **DONE**
5. **S1** ★ — redaction (gate for Phase 5 + X3; the trust feature — do it before any DevTools work)
6. **S2** — read-only mode (gate for Phase 5 mutations)
7. ✅ **R2** — self-heal daemon — **DONE** · **S3** — dry-run preview (pending; slot in anytime)
8. **X1 · X2 · X3** — record/replay (needs F1 + R1) · assertions · event stream (needs S1)
9. **D1 → D2 → D3 → D4 → D5** — DevTools expansion (all need S1; mutating ones need S2)

> You may reorder within a tier, but never ship a Phase-5 feature before S1, never ship X1 before R1, and
> never ship X3 before S1.

---

## PHASE 1 — Context & token efficiency (the moat) — roadmap §Phase 1

| ID | Build | Plugs in | Depends | Acceptance (the proof) |
|----|-------|----------|---------|------------------------|
| ✅ **C1** | `query` op: filter + **deterministic rank** the AXTree, return only matched refs + 1-line context (never the full tree) | daemon `query` · `bxw query` · MCP `query`; route snapshot `--find` through it | F2 | **DONE `fc79f60`** — golden ranker test + real headless-browser integration (`--role button --near "Total"` → checkout first); ranked & bounded |
| ✅ **C2** | Semantic action delta: one-line "what changed" from `ChangedNodes` ("dialog opened", "cart 1→2", "navigated /checkout"); raw diff behind `--verbose`/blob | extend `ActResult.TerseSummary` | F2 | **DONE `39a8859`** — golden map of synthetic `ChangedNodes` → summary strings; terse by default, full diff on demand |
| ✅ **C3** | Token-budgeted snapshot: `--budget N` returns top-N salient nodes; elided subtrees marked `[+k more — snapshot --ref eX]` | snapshot builder · `bxw snapshot --budget` | C1 | **DONE** — snapshot ≤ budget, elided nodes carry hint, `--ref` drills into the elided subtree |
| ✅ **C4** | Region memoization: hash ARIA-landmark regions into F2; re-snapshot emits "`nav` unchanged (cache `<hash>`)"; changed regions re-expand | snapshot builder + SessionModel | F2 | **DONE** — snapshot twice, only `main` changed → nav/footer come back as cache refs, main re-expands |
| ✅ **C5** | Episodic recall (`bxw recall`, C1-ranked/budgeted timeline of F2 notes+history) + close-out (`bxw done` distils notes to a durable 5-line takeaway) | daemon + `bxw recall`/`bxw done` · MCP `recall` (read-only) | C1, F2 | **DONE** — `recall` renders bounded ranked timeline honouring `--grep`/`--since`; `done` writes compact takeaway, raw notes intact |

## PHASE 2 — Reliability — roadmap §Phase 2

| ID | Build | Plugs in | Depends | Acceptance |
|----|-------|----------|---------|------------|
| ✅ **R1** | Smart waits: `--selector`/`--text`/`--gone`/`--stable`/`--network-idle`/`--load`/`--ms`, each `--timeout`; optional auto-wait before act | `bxw wait` + `actOn` opt-in | — | **DONE** — delayed-element fixture: `--selector` waits & succeeds, `--gone` waits for removal, timeout names the unmet condition |
| ✅ **R2** | Self-heal: on CDP read-loop error, bounded reconnect (backoff+jitter) re-resolving target via F2; typed error only after retries exhausted; log attempts | wrap `TabConn` dial/read | F2 | **DONE** — drop the ws mid-session → reconnect restores a working session; permanent failure returns typed `exit.Upstream` |

## PHASE 3 — Safety & trust (S1 before Phase 5) — roadmap §Phase 3

| ID | Build | Plugs in | Depends | Acceptance |
|----|-------|----------|---------|------------|
| **S1** ★ | `internal/redact`: pure `Redact(string)` + structured-field masking over a documented pattern set (password fields, bearer/JWT/API-key, high-entropy, `ghp_`/`AKIA…`/`xox…`/PEM). Route **every** outbound serializer through it. On by default; `--no-redact` needs a `perms` grant | all result serializers | — | table test: secrets masked, benign untouched; filled-password snapshot + JWT storage dump come back masked |
| **S2** | Read-only mode: `BXW_READONLY=1` / `--read-only` blocks every mutating op (click/fill/type/key/drag/upload/cookie-set/storage-write/mutating-cdp) with typed `exit.User`; reads allowed | gate mutating `dispatch` cases + cdp passthrough | — | read-only set → click/fill/cookie-set refuse, snapshot/query succeed |
| **S3** | Dry-run: `--dry-run` on mutating verbs resolves target + reports intended effect (element, URL, value) without dispatching input | each mutating run path (reuse `selector()`) | — | `--dry-run` click returns resolved target, page unchanged (snapshot identical) |

## PHASE 4 — Workflows & testing — roadmap §Phase 4

| ID | Build | Plugs in | Depends | Acceptance |
|----|-------|----------|---------|------------|
| **X1** | Record & replay: `bxw record --out flow.json` captures action sequence; `bxw replay` re-executes deterministically with R1 waits; resilient via F1 refs; fails loud at first diverging step | daemon + `bxw record`/`replay` | F1, R1 | record short flow on `data:` form, replay → end state matches; corrupt a step → fails loud at that step |
| **X2** | Assertions: `bxw assert [--text/--selector/--url/--count]` → exit 0/non-zero + terse reason; `--snapshot golden.txt` (update `--update`) | `bxw assert` | — | pass/fail paths on `data:` page; golden match + mismatch diff; exit codes are the contract |
| **X3** | Live event stream: subscribable MCP resources `bxw://events/<tab>/{console,network,dom}` honouring `resources/subscribe` + `notifications/resources/updated`; bounded + redacted | MCP resources over console/net ring buffers | S1 | captured console error → resource-updated notification; payload redacted + bounded |

## PHASE 5 — DevTools expansion (ALL output via S1; mutations via S2) — roadmap §Phase 5

| ID | Build | Plugs in | Depends | Acceptance |
|----|-------|----------|---------|------------|
| **D1** | Cookies: `bxw cookies [list\|set\|delete]` with `httpOnly`/`secure`/`samesite` flags; `--export jar.json` (perms-gated); set/delete are S2 mutations | CDP `Network`/`Storage` cookie APIs | S1, S2 | set → list (value masked) → delete; insecure attrs flagged |
| **D2** | Network: `--har out.har` (HAR 1.2) · `--body <reqID>` → blob cache · `bxw mock --url <glob> --status --body` via CDP Fetch (S2 mutation) | network ring buffer + CDP Fetch | S1, S2 | HAR validates against 1.2 shape; mock returns stubbed status/body |
| **D3** | Storage: extend `bxw storage` to IndexedDB listing + `--diff` since last call (F2/C4 style); writes S2-gated | CDP `DOMStorage`/`IndexedDB` | S1, S2, F2 | set keys → listing + diff; token value masked |
| **D4** | Service workers & Cache Storage: `bxw sw [list\|unregister]` · `bxw cachestorage list`; unregister S2-gated | CDP `ServiceWorker`/`CacheStorage` | S1, S2 | fixture SW → list shows it, unregister removes it (or skip-with-reason if CI can't host a SW) |
| **D5** | Security: `bxw security` reports TLS cert summary, security state, mixed-content, captured CSP violations; read-only | CDP `Security` + CSP/console events | S1 | CSP-meta + blocked-resource fixture → violation reported; cert + state surfaced |

---

## Next action for the implementation model

C1–C5 and R1–R2 are ✅ (`verify-roadmap` OK, tests green) — **Phases 1–2 are complete.**
Resume per the roadmap's anti-drift §1 (`git log`, `go test ./...`, `scripts/verify-roadmap.sh` — reality wins over the dashboard),
then start **S1** (WIP = 1) — secret / PII redaction (Phase 3; the trust feature and the gate for all of Phase 5).
