# WORK ORDER — bxw C3: token-budgeted / progressive snapshots

**For:** implementation model. **WIP = 1**, one Conventional Commit, CI must stay green, push to `origin/main`.
This is the detailed, code-grounded spec for roadmap row **C3** (Phase 1). The roadmap
([`WORKORDER_roadmap_beyond_bx.md`](WORKORDER_roadmap_beyond_bx.md) §C3) has the one-paragraph intent; this is
the implementation. Build order says C3 is next after C1 ✅ / C2 ✅.

**Goal:** cap snapshot token cost. `bxw snapshot <tab> --budget <N>` returns the most salient nodes within an
estimated token budget; pruned subtrees are marked `[+ k more — snapshot --ref eX]` so the agent drills down
only where it needs to. Default (no `--budget`) behaviour is unchanged.

## Current state / lessons to carry (do not relearn the hard way)

- **CI now has a gofmt gate** (`.github/workflows/ci.yml`, Linux leg: `gofmt -l .` fails the build). Run
  `gofmt -w .` before committing — an unformatted file turns CI red. (This is new since the fixes work order.)
- The **launch port race is fixed** (`--remote-debugging-port=0` + `DevToolsActivePort`), so browser
  integration tests are stable (~2s) — a live integration test for C3 is safe to add and run back-to-back.
- **Do NOT reuse C1's `RankNodes` for salience.** C1 ranks by *query relevance* (needs role/name/text params).
  C3 salience is a different axis — *structural importance* with no query. Keep them separate.

## The code you build on (grounded — read these first)

- `internal/daemon/snapshot.go`
  - `type SnapNode struct { Ref string; Depth int; Role, Name, Value string; Props []string }` — the snapshot
    is a **flat slice** in document order; `Depth` encodes the tree.
  - `buildSnapshot(...) ([]SnapNode, map[string]cdp.BackendNodeID, map[string]RefDescriptor)` (line ~77) —
    produces the flat list.
  - `func (s Snapshot) Text() string` (line ~218) — renders each node as
    `<"  "×Depth><ref> <role> "name" value="…" [props]`. **This rendered line is the token-estimate unit.**
- `internal/daemon/diff.go`
  - `FilterSubtree(nodes []SnapNode, targetRef string) []SnapNode` (line ~157) — returns a ref's subtree.
    **This is the drill-down**: `snapshot --ref eX` already scopes to a subtree. Reuse it; don't reinvent.
- Wiring points: snapshot daemon op `internal/daemon/server.go` (`case "snapshot":` ~line 348);
  CLI `runSnapshot` `internal/app/app.go` (~line 353, already parses `--ref`, `--find`, `--full`, `--out`);
  MCP `snapshot` tool in `internal/app/mcp.go`.

## Design

### 1. Salience tiers (new — define explicitly, document them)
Score each `SnapNode` by role into a stable, documented priority (highest first). bxw has **no** existing
interactive-role set, so add one (e.g. `internal/daemon/salience.go`):

- **Tier 1 — interactive (keep first):** `button`, `link`, `textbox`, `textarea`, `combobox`, `listbox`,
  `checkbox`, `radio`, `switch`, `slider`, `spinbutton`, `menuitem`, `menuitemcheckbox`, `tab`, `searchbox`,
  `option` (match case-insensitively; keep the set in one documented `var`).
- **Tier 2 — structure:** `heading`, and ARIA landmarks `navigation`, `main`, `banner`, `contentinfo`,
  `complementary`, `region`, `form`, `search`.
- **Tier 3 — text/other:** `StaticText`, `paragraph`, everything else.

Salience is for **inclusion priority only** — NEVER reorder the output. The kept nodes render in original
document order so an agent's spatial/logical reading is preserved and refs stay where they belong.

### 2. Budget-aware pruning (pure, unit-testable)
Add a pure function, e.g.:
```go
// BudgetSnapshot keeps the most salient nodes within an estimated token budget,
// in document order, and reports how many descendants were pruned under each kept
// node (keyed by that node's ref) so the caller can mark drill-down points.
func BudgetSnapshot(nodes []SnapNode, budget int) (kept []SnapNode, elided map[string]int)
```
- **Token estimate:** render each node's line as `Snapshot.Text()` does and use `len(line)/4` (document this
  char/4 heuristic — it is an estimate, not a tokenizer). Sum as you include.
- **Algorithm:** always keep Tier-1, then Tier-2, then Tier-3, each in document order, including nodes until
  the running estimate would exceed `budget`. Always keep a pruned node's **nearest kept ancestor** (walk
  `Depth`) so the tree stays connected and the drill-down hint has a real ref to point at. For each kept node,
  count descendants that were dropped → `elided[ref]`.
- **Determinism:** identical input + budget → identical output. Tie-break strictly by original index.

### 3. Render the elided markers
Extend the snapshot rendering (don't fork `Text()` wholesale — add a budgeted path): after a kept node with
`elided[ref] > 0`, emit one indented line:
```
  [+ 7 more — snapshot --ref e14]
```
(ref = that kept node's ref, count = `elided[ref]`).

### 4. Drill-down composes with `--ref`
`snapshot <tab> --ref e14` already returns e14's subtree via `FilterSubtree`. Make `--budget` and `--ref`
compose (budget a drilled subtree). Verify the elided-marker ref actually resolves with `--ref`.

### 5. Wiring
- `runSnapshot` (app.go): parse `--budget N` (and `--budget=N`); `N<=0` or absent ⇒ current behaviour.
- snapshot op (server.go): thread `budget` through; call `BudgetSnapshot` after `buildSnapshot`/existing filters.
- MCP `snapshot` tool (mcp.go): add `budget` (integer) to the input schema.
- `bxw --help` / usage: document `--budget`.

## Tests (right pyramid level; golden + one live)

- **Golden unit (`salience_test.go` / `snapshot_test.go`):**
  - `BudgetSnapshot` over a synthetic `[]SnapNode`: estimated tokens of `kept` ≤ budget; a pruned container's
    kept ancestor has `elided[ref] == k`; Tier-1 interactive nodes survive a tight budget before Tier-3 text;
    output stays in document order; deterministic across two runs.
  - Rendering: a budgeted snapshot contains `[+ k more — snapshot --ref eX]` for the elided container.
  - Drill-down: `FilterSubtree(nodes, "eX")` returns the elided subtree (the hint is actionable).
- **Live integration (safe now — launch race fixed):** `httptest` page with a long list / deep nav; headless
  launch; `snapshot --budget <small>` returns a bounded snapshot with a `[+ k more …]` marker; a second
  `snapshot --ref <that ref>` expands it. Mirror `TestC1QueryIntegration`'s setup; `defer` kill **and wait**
  for the browser to exit.

## Definition of done

Daemon op + CLI `--budget` + MCP `budget` wired · golden unit tests + one live integration test, green ·
`go vet` / `GOOS=linux go build` / `go test ./...` green · **`gofmt -l .` clean (CI gate will enforce)** ·
README (`snapshot --budget`, drill-down) + CHANGELOG `[Unreleased]` + roadmap dashboard C3 ✅ + exec-plan C3 ·
`scripts/verify-roadmap.sh` → `VERIFY OK` · one Conventional Commit `feat(context): C3 token-budgeted snapshots`.
Paste the real `go test` line as Evidence. Be honest in the summary about what CI covers vs the interactive
browser bits.

## Verification the reviewer (me) will run
`gofmt -l` on committed blobs (must be empty — the gate makes this non-negotiable), `go vet`, native
`go test ./...`, the budgeted-snapshot golden assertions, the live integration test **back-to-back**
(`-count=2`, must not flake), `verify-roadmap`, and CI green on both OSes on the exact commit.
