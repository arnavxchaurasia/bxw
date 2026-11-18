# bxw

[![CI](https://github.com/arnavxchaurasia/bxw/actions/workflows/ci.yml/badge.svg)](https://github.com/arnavxchaurasia/bxw/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/arnavxchaurasia/bxw?include_prereleases&sort=semver)](https://github.com/arnavxchaurasia/bxw/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/arnavxchaurasia/bxw)](go.mod)
[![Platform: Windows](https://img.shields.io/badge/platform-windows-0078D6)](#)

**Drive the Windows Chromium browser you already have open — by profile, over the Chrome DevTools Protocol (CDP) — from AI coding agents.**

`bxw` lets AI coding agents (Claude Desktop, Cursor, Antigravity, Codex, Cline) interact with real, authenticated browser sessions or isolated sandboxes. It extracts token-efficient accessibility snapshots with sequential element references (`e1`, `e2`, `e3`), then drives actions (`click`, `fill`, `type`, `key`, `eval`) with zero brittle coordinate guessing.

Built in clean-room Go, `bxw` runs as a single static binary with no external runtimes, zero telemetry, and an OS-agnostic hexagonal core.

![bxw demo — launch, list tabs, snapshot, eval, stop](docs/assets/demo.svg)

---

## Features

- **Windows-First Discovery**: Automatically detects Microsoft Edge, Brave, Google Chrome, Vivaldi, and Chromium via registry `App Paths` and discovers all user profiles (`Default`, `Profile 1`, etc.) from `Local State`.
- **Browser-Independent Persistent Profiles (`bxw login`)**: Bypasses Chromium 136's default-profile remote debugging block. Users log in once in an isolated, persistent profile directory (`%LOCALAPPDATA%\bxw\profiles\<browser>`). Cookies and authentication tokens persist across runs without touching the main personal browser directory.
- **Token-Efficient Diff Snapshots**: Emits compact deltas (`+`, `-`, `~`) against cached baselines (`state/<browser>/<tab>.json`) or returns `unchanged`, slashing agent token usage by 10–50× on iterative turns.
- **Scoping & Filtered Queries**: `--ref <ref>` captures a specific subtree; `--find <query>` locates matching nodes and their ancestors.
- **Content-Addressed Blob Cache (`bxw://cache/<hash>`)**: Large artifacts (screenshots, full HTML) are saved to disk with SHA-256 addresses and referenced via MCP resource URIs rather than blowing context windows with megabytes of base64 data.
- **Risk-Gated Capabilities & Permissions (`bxw perms`)**: W3C-style origin permission gating (`form_submit`, `purchase`, `delete`, `download`, `enter_credentials`, `cross_origin_nav`) preventing unintended mutations or data egress.
- **Audit Logging & Context Store**: Append-only navigation log (`history/<browser>.jsonl`) and durable agent session store (`context/<session>.json`) for cross-turn memory and recovery.
- **Accessibility Snapshots**: Walks the DOM AXTree, pruning noise (`generic`, `none`, inline text nodes) and assigns concise references:
  ```text
  [e1] heading "Sign In"
  [e2] textbox "Email address"
  [e3] textbox "Password" [focused]
  [e4] checkbox "Remember me" [checked]
  [e5] button "Submit"
  ```
- **Ref-Based Precision Actions**: Tag elements in DOM, compute center coordinates, and dispatch real mouse and keyboard events with terse 1-line feedback (`clicked e5 -> 2 nodes changed`):
  - `bxw click <tab> e5`
  - `bxw fill <tab> e2 user@example.com`
  - `bxw type <tab> s3cr3t --submit Enter`
  - `bxw key <tab> Enter`
- **Model Context Protocol (MCP) Server**: Built-in JSON-RPC 2.0 stdio server exposing 28 tools (full `@facets-cloud/bx` v0.4.1 parity) + resource handlers to Claude Desktop, Cursor, and Antigravity.
- **AF_UNIX Singleton Daemon**: Manages persistent WebSocket multiplexing to CDP behind a lightweight background daemon over Windows AF_UNIX domain sockets.

---

## Installation

### Option 1: via npm / npx (Recommended for AI agents)
```bash
# Run immediately with npx
npx bxw doctor

# Or install globally
npm install -g bxw
```

### Option 2: Pre-compiled Windows Binaries
Download the latest pre-compiled archive for your architecture from the [GitHub Releases](https://github.com/arnavxchaurasia/bxw/releases) page:
- **x64 (Intel/AMD)**: `bxw_windows_amd64.zip`
- **ARM64 (Surface / Snapdragon)**: `bxw_windows_arm64.zip`

Extract `bxw.exe` and place it in your `PATH` (e.g. `%USERPROFILE%\bin` or `C:\Windows\System32`).

### Option 3: Build from Source
```powershell
git clone https://github.com/arnavxchaurasia/bxw.git
cd bxw
go build -o bxw.exe .
```

---

## Quickstart

### 1. Check Your Machine
```powershell
bxw doctor
```
Inspects all detected Chromium browsers, profile directories, running instances, and daemon statuses.

### 2. Isolated Mode (Sandbox)
```powershell
# Launch an isolated Edge session
bxw launch edge https://example.com

# List open tabs — note the tab ID (e.g. F0DBC61D)
bxw list tabs edge

# Take an accessibility snapshot (pass the tab ID, not the browser name)
bxw snapshot F0DBC61D

# Click an element by its ref (tab ID first, then the ref)
bxw click F0DBC61D e7

# Stop the daemon & close the instance
bxw stop edge
```

### 3. Real-Profile Mode (Your Logged-in Browser)
```powershell
# 1. Enable remote debugging flag in Edge's Local State
bxw setup edge

# 2. Restart Edge normally
# 3. Open a tab in your "Default" or "Profile 1" profile
bxw open edge https://github.com --profile "Default"

# 4. Interact with your logged-in session
bxw snapshot edge
```
*To revert the debugging flag at any time:*
```powershell
bxw setup edge --revert
```

---

## CLI Reference

| Command | Description |
|---|---|
| `bxw doctor` | Self-test machine environment, browsers, profiles, and daemons |
| `bxw list browsers` | List all installed Chromium browsers, binary paths, and user data directories |
| `bxw list profiles <browser>` | List all profile directories and human-readable names for a browser |
| `bxw launch <browser> [url]` | Launch an isolated temporary agent instance on a dynamic CDP port |
| `bxw login <browser> [url]` | Open persistent profile headed to log in once (persisting session across runs) |
| `bxw list tabs [browser]` | List open tabs (tab ID, profile, title, URL) across connected instances |
| `bxw open <url> [--profile <name>] [--follow]` | Open a URL in a new tab, optionally targeting a specific profile |
| `bxw close-tab <tab>` | Close a single open tab |
| `bxw goto <tab> <url> [--follow]` | Navigate the tab to a new URL |
| `bxw wait <tab> [--selector s\|--text t\|--load\|--ms N]` | Wait for selector, text, load completion, or sleep |
| `bxw read <tab> [--ref e12] [--max 20000]` | Read readable text content from the page or subtree |
| `bxw dialog <tab> <accept\|dismiss> [--text t]` | Respond to JavaScript alert, confirm, or prompt dialog |
| `bxw console <tab> [--level lvl] [--limit N]` | Tail captured console and error logs |
| `bxw network <tab> [--filter s] [--id reqID]` | Inspect captured network requests and responses |
| `bxw storage <tab>` | Inspect cookies, localStorage and sessionStorage |
| `bxw performance <tab>` | Audit Core Web Vitals (LCP, FCP, CLS, TTFB, DOM size, heap) |
| `bxw emulate <tab> [flags]` | Emulate mobile/desktop, network throttling, CPU, geolocation, color scheme |
| `bxw snapshot <tab> [flags]` | Print indented AXTree element snapshot with `e1`, `e2` refs |
| `bxw query <tab> [flags]` | Filter and rank elements by role, name, text, and proximity |
| `bxw screenshot <tab> [flags]` | Capture viewport PNG, saving to content-addressed cache |
| `bxw click <tab> <ref\|sel> [--double]` | Click an element by its snapshot reference or CSS selector |
| `bxw hover <tab> <ref\|sel>` | Hover mouse over an element |
| `bxw drag <tab> <from> <to>` | Drag element to destination element |
| `bxw upload <tab> <ref\|sel> <file>...` | Set files on an input[type=file] element |
| `bxw fill <tab> <ref\|sel> <val>...` | Focus, clear, and populate text into an input or textarea |
| `bxw type <tab> <text> [--submit <k>]` | Type characters sequentially into active element, optional submit key |
| `bxw key <tab> <combo>` | Dispatch a keypress (e.g. `Enter`, `Tab`, `Escape`, `Control+A`) |
| `bxw eval <tab> <expr>` | Evaluate a JavaScript expression in the tab's execution context |
| `bxw cdp <tab> <Domain.method> [--params <json>]` | Send any raw Chrome DevTools Protocol command |
| `bxw perms [list\|grant\|revoke]` | Inspect and manage capability permissions |
| `bxw history [browser] [flags]` | Query navigation history (--since, --grep, --profile) |
| `bxw context [browser] [--session id]` | View session context summary |
| `bxw clean [--all]` | Clean cache, stale state, and logs |
| `bxw setup <browser> [--revert]` | Toggle `devtools.remote_debugging.user-enabled` in `Local State` |
| `bxw stop <browser>` | Terminate the background daemon and disconnect CDP sessions |
| `bxw mcp` | Speak Model Context Protocol (MCP) over stdio |
| `bxw mcp install [client]` | Automatically register the `bxw` MCP server with agent configurations |

---

## Model Context Protocol (MCP) Setup

`bxw` includes a zero-dependency, JSON-RPC 2.0 stdio MCP server exposing full tool-for-tool parity with `@facets-cloud/bx` v0.4.1 (28 tools):

- **Setup**: `doctor`, `list_browsers`, `setup`, `launch`
- **Read**: `list_profiles`, `list_tabs`, `snapshot`, `query`, `read`, `screenshot`, `eval`, `wait`
- **Act**: `open`, `goto`, `click`, `hover`, `drag`, `fill`, `type`, `key`, `upload`, `dialog`, `close_tab`
- **Debug**: `console`, `network`, `storage`, `performance`, `emulate`, `cdp`

### Automatic Setup
Run:
```powershell
bxw mcp install
```
`bxw` automatically detects and updates the configuration files for **Claude Desktop**, **Cursor**, and **Antigravity**.

### Manual Setup

#### Claude Desktop
Add to `%APPDATA%\Claude\claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "bxw": {
      "command": "npx",
      "args": ["-y", "bxw", "mcp"]
    }
  }
}
```
(If `bxw` is installed and on your `PATH`, use `"command": "bxw", "args": ["mcp"]` instead.)

#### Cursor
Add to `.cursor/mcp.json` (or Cursor Settings → Features → MCP):
```json
{
  "mcpServers": {
    "bxw": {
      "command": "npx",
      "args": ["-y", "bxw", "mcp"]
    }
  }
}
```

#### Antigravity
Add to `%USERPROFILE%\.gemini\antigravity\mcp_config.json`:
```json
{
  "mcpServers": {
    "bxw": {
      "command": "npx",
      "args": ["-y", "bxw", "mcp"]
    }
  }
}
```

---

## Architecture & Design

```
┌────────────────────────────────────────────────────────┐
│             AI Agent (Claude / Cursor / CLI)           │
└───────────────────────────┬────────────────────────────┘
                            │ (stdio MCP / CLI invocations)
┌───────────────────────────▼────────────────────────────┐
│                    bxw CLI & MCP Server                │
└───────────────────────────┬────────────────────────────┘
                            │ (AF_UNIX socket RPC)
┌───────────────────────────▼────────────────────────────┐
│                     bxw Daemon                         │
│  - WebSocket multiplexing over github.com/coder/websocket│
│  - Typed CDP protocol using github.com/chromedp/cdproto │
│  - AXTree walker with role/aria filtering              │
│  - Action dispatch (DOM node resolution & synthetic input)│
│  - Profile correlation via data: URL nonce probes      │
└───────────────────────────┬────────────────────────────┘
                            │ (CDP over WebSocket)
┌───────────────────────────▼────────────────────────────┐
│                 Chromium Browser                       │
│    (Microsoft Edge / Brave / Google Chrome / etc.)     │
└────────────────────────────────────────────────────────┘
```

### Hexagonal OS Abstraction (`osport.Platform`)
The core automation logic never branches on `runtime.GOOS`. All Windows-specific operations (Registry queries, App Paths lookups, `Local State` parsing, and Toolhelp32 process snapshots) are sequestered in `internal/osport/platform_windows.go`. A stub implementation (`platform_other.go`) ensures clean cross-compilation on Linux and macOS for testing and CI.

---

## Why bxw is safe

`bxw` is built so an AI agent can touch a real, logged-in browser without becoming a liability:

- **Zero telemetry.** No outbound internet requests, no analytics, no phone-home. It talks only to the local CDP endpoint of a browser you launched.
- **No admin rights.** Runs entirely in user space; never edits system or security policy (HKLM).
- **Profile isolation.** Agent sessions default to isolated, persistent profiles under `%LOCALAPPDATA%\bxw\profiles\` — your everyday browser profile is never driven unless you explicitly attach to it. Teardown only kills browser processes bound to bxw's own profile dir, never your personal browser.
- **Risk-gated actions.** Sensitive capabilities (`form_submit`, `purchase`, `delete`, `download`, `enter_credentials`, `cross_origin_nav`) are permission-gated per origin via `bxw perms`.
- **Challenges are detected, never defeated.** `bxw` will **not** solve CAPTCHAs, bypass "I'm not a robot" checks, or spoof fingerprints/stealth. It detects a challenge and hands control back to a human. This is a permanent design line, not a missing feature.

See [SECURITY.md](SECURITY.md) for the loopback threat model and the Isolated-Agent vs Real-Profile-Attach distinction.

---

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for code style, testing workflows, and cross-compilation requirements, and our [Code of Conduct](CODE_OF_CONDUCT.md). Release history is in [CHANGELOG.md](CHANGELOG.md).

---

## License

[MIT License](LICENSE) © 2026 Arnav Chaurasia
