# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **S1: Secret and PII redaction layer (`--no-redact`, `internal/redact`).**
  Implemented single redaction pass applied to all outbound daemon serializers
  (`snapshot`, `read`, `query`, `console`, `network`, `storage`). Masks OWASP credential
  and token classes: GitHub personal access tokens (`ghp_`, `github_pat_`), AWS access
  keys (`AKIA...`), Slack tokens (`xox[baprs]-`), Google API keys (`AIza...`), Bearer
  and JWT tokens, PEM private key blocks, URL basic auth, sensitive parameters (`password=...`,
  `secret=...`, etc.), password input values, sensitive cookies/localStorage entries, and
  standalone high-entropy secrets (Shannon entropy >= 4.5 bits/char). Enabled by default;
  opt-out flag `--no-redact` is gated by `perms.Default().Check(origin, CapNoRedact)` and
  refuses with a typed error unless granted. Added `--no-redact` flag to CLI verbs and
  `no_redact` parameter to MCP tools.
- **R2: Supervised auto-reconnect and self-healing daemon.**
  Added supervised reconnection with exponential backoff and jitter on transient CDP connection drops.
  Re-resolves target tabs using the durable F2 SessionModel (matching by tab ID or last known URL),
  re-enables DevTools domains, restores subscriptions, and updates daemon session state seamlessly.
  Diagnostics are logged to `%LOCALAPPDATA%\bxw\logs\reconnect.log`. Surfaces a structured, typed
  `exit.Upstream` error if reconnect retries are exhausted.
- **R1: Smart wait conditions and actionability auto-waiting (`bxw wait`, `click --wait`, `fill --wait`).**
  Extended `bxw wait` with web-first predicates: `--gone <selector|ref>` (waits for removal/detachment),
  `--stable` (waits for DOM mutations to quiet down for 100ms via `MutationObserver`), and
  `--network-idle` (tracks in-flight CDP Network requests until 0 in-flight for 500ms).
  Wait failures return clean, typed timeout errors explicitly naming the unmet condition.
  Added optional `--wait` flag to `click` and `fill` CLI verbs and `wait` parameter to MCP tools
  for auto-waiting on element actionability (attached, visible, non-zero size, enabled).
- **C5: Episodic recall + close-out distillation (`bxw recall`, `bxw done`).**
  Episodic memory timeline merging dated notes (`context/<session>.md`) and navigation
  history (`history/<browser>.jsonl`) into a compact, deduplicated timeline ranked
  by relevance and capped by `--limit`. Output prints lazy pointers to note files and
  cached resources rather than dumping raw transcripts. `bxw done` distils the session's
  notes into a durable ~5-line takeaway persisted to `SessionModel` and append-only
  markdown narrative. Read-only MCP tool `recall` and mutating MCP tool `done`.
- **C4: Region memoization (`bxw snapshot`).** ARIA landmark regions (navigation,
  main, banner, contentinfo, complementary, region, form, search) are hashed into
  the F2 SessionModel. On subsequent snapshots, unchanged regions are emitted as
  concise 1-line cache references (`<ref> <role> ["name"] unchanged (cache "<hash>")`),
  while modified regions re-expand normally, drastically cutting token overhead
  for static chrome and navigation bars.
- **C3: Token-budgeted / progressive snapshots (`bxw snapshot --budget <N>`).**
  Capped snapshot token costs by scoring nodes into salience tiers (Tier 1: interactive,
  Tier 2: structure, Tier 3: text/other) using a char/4 token estimate heuristic.
  Pruned subtrees emit actionable drill-down hints (`[+ k more — snapshot --ref eX]`)
  which compose directly with `snapshot --ref`. Added `--budget` flag to `snapshot`
  CLI verb and `budget` parameter to `snapshot` MCP tool.
- **C2: Semantic action deltas.** `actOn` now classifies baseline node diffs
  into concise 1-line semantic change summaries ("dialog opened: Confirm (2 buttons)",
  "cart badge 1→2", "notification: ...", "navigated to ...", "+3 elements").
  Emitted by default in `ActResult.TerseSummary`, with raw diff cached to content-addressed
  blob storage (`bxw://cache/...`) and viewable via `--verbose` across mutating verbs
  (`click`, `fill`, `type`, `key`, `hover`, `drag`, `upload`).
- **C1: Ranked retrieval / query API (`bxw query`).** Filter and deterministically
  rank accessibility elements by ARIA role, accessible name, text, and proximity
  to another label (`--near`), returning bounded refs with 1-line context to cut
  token use. Promoted `snapshot --find` to use the same ranking model (including role matching).
  Added `query` CLI verb and `query` MCP tool.

## [0.1.0] - 2026-11-15

First public release. Windows-native, clean-room Go. Drives Chromium browsers
(Edge, Brave, Chrome, Vivaldi, Chromium) by profile over the Chrome DevTools
Protocol, for AI coding agents.

### Added
- **CLI + daemon architecture.** Single static binary; an AF_UNIX singleton
  daemon multiplexes CDP WebSocket sessions behind the CLI and MCP server.
- **Windows browser & profile discovery.** Detects installed Chromium browsers
  via registry `App Paths`; enumerates profiles from each browser's `Local State`.
- **Browser-independent persistent profiles (`bxw login`).** Works around
  Chromium 136's default-profile remote-debugging block by using isolated,
  persistent profile dirs under `%LOCALAPPDATA%\bxw\profiles\<browser>`; sign in
  once and the session persists across runs without touching the real profile.
- **Token-efficient diff snapshots.** Accessibility-tree snapshots with
  sequential refs (`e1`, `e2`, …) emitted as `+`/`-`/`~` deltas against cached
  baselines (or `unchanged`), cutting agent token use sharply on iterative turns.
- **Ref-based precision actions.** `click`, `fill`, `type`, `key`, `hover`,
  `drag`, `upload` by snapshot ref or CSS selector, with terse 1-line results.
- **Content-addressed blob cache.** Large artifacts (screenshots, full HTML)
  saved to disk and referenced via `bxw://cache/<sha256>` MCP resource URIs.
- **Risk-gated permissions (`bxw perms`).** Origin-scoped capability gating
  (`form_submit`, `purchase`, `delete`, `download`, `enter_credentials`,
  `cross_origin_nav`).
- **Audit log & context store.** Append-only navigation history and a durable
  per-session context store.
- **Full bx v0.4.1 MCP parity — 28 tools** over a zero-dependency JSON-RPC 2.0
  stdio server: `list_browsers`, `list_tabs`, `open`, `close_tab`, `goto`,
  `wait`, `read`, `console`, `network`, `storage`, `performance`, `snapshot`,
  `screenshot`, `click`, `hover`, `drag`, `upload`, `dialog`, `fill`, `type`,
  `key`, `eval`, `cdp`, `emulate`, `doctor`, `setup`, `launch`, `list_profiles`.
  `bxw mcp install` registers the server with Claude Desktop, Cursor, and Antigravity.
- **Debug tooling.** `console`, `network`, `storage`, `performance` (Core Web
  Vitals), `emulate` (device/network/CPU/geo/color-scheme), and `cdp` (raw
  DevTools passthrough).
- **Hexagonal OS seam (`osport.Platform`).** All Windows-specific code is
  isolated; a `!windows` stub keeps the repo cross-compilable for CI.
- **Packaging.** npm dispatch (`npx bxw`) with per-arch subpackages, plus
  windows-latest + ubuntu-latest CI and a tag-triggered release pipeline that
  publishes checksummed win32 x64/arm64 archives.

### Fixed
- **CDP singleton collision on relaunch (F2).** `stop` previously killed only the
  daemon, leaving the browser alive; the next launch handed off to the existing
  instance and silently ignored the new debug port. Launch now reuses a live
  profile's `DevToolsActivePort`, and `stop` tears down every browser process
  bound to the bxw profile dir (and only those).
- **`fill` on native picker inputs** (`date`/`time`/`datetime-local`/`month`/
  `week`/`color`/`range`) now dispatches bubbling `input`+`change` events and
  verifies the value stuck, instead of silently reporting a false success.
- **`open` on a fresh profile** no longer times out on the first-run/welcome tab.
- **`close_tab` on the last tab** now refuses with clear guidance instead of
  letting the browser exit and orphaning the CDP session.
- **Release binaries are now version-stamped** (`bxw/internal/app.Version`); the
  release build previously stamped a non-existent `main.version` and shipped as
  `0.0.0-dev`.

[Unreleased]: https://github.com/arnavxchaurasia/bxw/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/arnavxchaurasia/bxw/releases/tag/v0.1.0
