# WORK ORDER — bx PR #3: Windows support

**For:** implementation model (follow exactly; the design and the hard syscall code are done below and verified to build).
**Repo:** `Facets-cloud/bx` via fork `arnavchaurasia-knp/bx`.
**Branch:** `feat/windows-support` (new, off `main` — NOT off the consent branch).
**Identity:** `git config user.name "arnavxchaurasia"`, `git config user.email "arnav.chaurasia@facets.cloud"`.
**gh account:** ensure `gh auth switch -u arnavchaurasia-knp` before pushing (it drifts to the personal account; a push as `arnavxchaurasia` gets 403).

> A verified core already exists (builds on windows+darwin+linux, `go vet` clean). Arnav can push it as a starting point. If not, the files below ARE that exact verified code — transcribe them.

## Goal & scope

Make bx **build and run on Windows** (Edge/Brave/Chrome/Vivaldi/Chromium), additive and clean-room-faithful to bx's architecture. bx is macOS-only today; `internal/browser/agent.go` and `internal/daemon/client.go` use POSIX-only `syscall` calls that don't compile on Windows.

**In scope:** compile on Windows; browser/profile discovery on Windows; process control (running/launch/quit); the approval dialog on Windows; a Windows CI job; test isolation so that CI is green; docs.

**Explicitly OUT of scope (say so in the PR, offer as follow-ups):**
- Distribution: npm platform package (`npm/bx-win32-x64`), the `bx upgrade` self-update (`setup.go` fetches `bx_darwin_*.tar.gz`), and Windows release artifacts in `release.yml`. This PR ships "builds and runs from source / `go install`"; packaging is a separate PR.
- One PR, one feature — Pramodh asked for focused PRs.

## bx-compatibility rules (do not break these)

1. **macOS behavior unchanged, byte for byte.** Everything new is in build-tagged files (`_windows.go` / `_posix.go` / `_darwin.go`) or thin platform-function calls. Prove it: `GOOS=darwin go build ./... && GOOS=darwin go vet ./... && GOOS=darwin go test -c ./...` must be identical in behavior to `main`.
2. **No new module dependency.** Do NOT add `golang.org/x/sys` (bx has zero of it today — `grep golang.org/x/sys go.sum` is empty). Windows process control uses the built-in tools (`tasklist`, `taskkill`), PowerShell, and the standard `syscall` package's `CreationFlags` — exactly the way bx already shells out to `open`, `osascript`, `ps`. Lock uses `kernel32` via `syscall.NewLazyDLL` (no new import).
3. **Same protocol/CLI/MCP surface.** bx's Unix-domain socket IPC stays (AF_UNIX works on Windows 10 1803+ and in Go). No new ops, flags, or tools.
4. **Match bx's comment voice** — lowercase-leaning, terse, explains the "why", "the user"/"an agent". The code below already does; keep it.

## What Pramodh's consent review tells us he'll want here (bake these in)

- **The approval dialog is the security boundary.** On macOS it's an osascript dialog outside the browser that a hijacked agent can't answer, defaults to Deny, and times out to Deny. The **Windows dialog must give the same guarantees** (separate process, default = No, 60s timeout → No, message passed via env not script text so a page can't inject it). This is the most security-sensitive file in the PR — call it out in the PR body.
- **Honest testing / real execution.** bx's CI is `macos-latest` only, so Windows would ship **untested**. This PR **adds a `windows-latest` CI job** (TASK 7) so build+vet+test actually run on Windows. In the PR body, state what CI covers vs the interactive bits (the real MessageBox appearing, a live browser over CDP) that need a manual spot-check on a Windows box.
- **Don't drop errors** (`_ =`). The one spot that intentionally ignores a result (Windows `unlockFile`) is commented as non-fatal; everything else returns/handles errors.
- **Docs.** README platform line + SECURITY.md approval mechanism (TASK 8).
- **No unused/dead code**, minimal diff, edge cases handled (browser not installed, `%LOCALAPPDATA%` unset, stale PID / PID reuse — the quit path verifies the process command line before killing).

## Tracking table (keep updated)

| # | Item | Status |
|---|------|--------|
| 1 | Build-tagged browser table (Windows paths + exe resolution) | [x] |
| 2 | Process seam: `Running`/`detach`/`startBrowser`/`quitProcess` (posix + windows) | [x] |
| 3 | Daemon lock/detach seam (flock→LockFileEx, Setsid→CreationFlags) | [x] |
| 4 | Windows approval dialog (`approve_windows.go`) + restrict `approve_other.go` | [x] |
| 5 | Rewire `agent.go` + `client.go` + `browser.go` to the seams | [x] |
| 6 | Test isolation: guard profile-writing tests, cross-platform config-home helper | [x] |
| 7 | CI: add `windows-latest` to `ci.yml`; green on Windows | [x] |
| 8 | Docs: README platform + SECURITY.md Windows dialog + permission-message polish | [x] |
| 9 | Windows unit tests (table/expandEnv/exe resolution) | [x] |

---

## TASK 1 — browser table per OS

Move the macOS `table` out of `internal/browser/browser.go` and add the Windows + fallback tables.

**Edit `internal/browser/browser.go`** — replace the whole `var table = []Browser{ ... }` block (the macOS entries) with this comment:

```go
// table lists the supported browsers for the current OS. It is defined per
// platform, since the data dirs and executables differ: table_darwin.go for
// macOS, table_windows.go for Windows (table_other.go elsewhere).
```

**New `internal/browser/table_darwin.go`** — `//go:build darwin`, holding the exact macOS table that was in browser.go (Brave/Chrome/Chromium/Edge/Arc/Vivaldi, `DataDir` relative to `$HOME`, `App` the `/Applications/...` bundle exe, the `*://inspect/#remote-debugging` Inspect). (Copy the entries verbatim from the current `browser.go`.)

**New `internal/browser/table_windows.go`** — `//go:build windows`:

```go
//go:build windows

package browser

import (
	"os"
	"path/filepath"
	"strings"
)

// table lists the supported browsers on Windows. The data dirs live under
// %LOCALAPPDATA% (absolute, so Dir() returns them as-is); App is resolved to
// the first install path that exists. Paths verified on Windows 11 build
// 26300 — Edge installs under the 32-bit Program Files even though it is a
// 64-bit app, so both roots are probed.
var table = winTable()

func winTable() []Browser {
	defs := []struct {
		name, label, data, inspect string
		exes                       []string
	}{
		{"edge", "Microsoft Edge", `Microsoft\Edge\User Data`, "edge://inspect/#remote-debugging",
			[]string{`%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe`, `%ProgramFiles%\Microsoft\Edge\Application\msedge.exe`}},
		{"brave", "Brave", `BraveSoftware\Brave-Browser\User Data`, "brave://inspect/#remote-debugging",
			[]string{`%ProgramFiles%\BraveSoftware\Brave-Browser\Application\brave.exe`, `%ProgramFiles(x86)%\BraveSoftware\Brave-Browser\Application\brave.exe`}},
		{"chrome", "Chrome", `Google\Chrome\User Data`, "chrome://inspect/#remote-debugging",
			[]string{`%ProgramFiles%\Google\Chrome\Application\chrome.exe`, `%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe`, `%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe`}},
		{"vivaldi", "Vivaldi", `Vivaldi\User Data`, "vivaldi://inspect/#remote-debugging",
			[]string{`%LOCALAPPDATA%\Vivaldi\Application\vivaldi.exe`, `%ProgramFiles%\Vivaldi\Application\vivaldi.exe`}},
		{"chromium", "Chromium", `Chromium\User Data`, "chrome://inspect/#remote-debugging",
			[]string{`%LOCALAPPDATA%\Chromium\Application\chrome.exe`}},
	}
	local := os.Getenv("LOCALAPPDATA")
	out := make([]Browser, 0, len(defs))
	for _, d := range defs {
		out = append(out, Browser{
			Name:    d.name,
			Label:   d.label,
			DataDir: filepath.Join(local, d.data),
			App:     firstExisting(d.exes),
			Inspect: d.inspect,
		})
	}
	return out
}

// firstExisting expands each candidate and returns the first that is present,
// else the first candidate (so messages still name a sensible path).
func firstExisting(cands []string) string {
	var first string
	for _, c := range cands {
		e := expandEnv(c)
		if first == "" {
			first = e
		}
		if _, err := os.Stat(e); err == nil {
			return e
		}
	}
	return first
}

// expandEnv replaces %VAR% with the environment value, for the install paths.
func expandEnv(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		j := strings.IndexByte(s, '%')
		if j < 0 {
			b.WriteByte('%')
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(os.Getenv(s[:j]))
		s = s[j+1:]
	}
}
```

**New `internal/browser/table_other.go`** — `//go:build !darwin && !windows`:

```go
//go:build !darwin && !windows

package browser

// table is empty on systems bx has no browser layout for yet (Linux). bx
// still builds there, but no browser is found; a port adds a table like
// table_darwin.go or table_windows.go.
var table = []Browser{}
```

## TASK 2 — process seam (`internal/browser`)

**New `internal/browser/proc_posix.go`** — `//go:build !windows`. Move the current `Running()` (SingletonLock symlink + `syscall.Kill`) here, plus `detach`, `startBrowser` (the `open -a` logic from the current `Start()`), and `quitProcess` (the `ps -o command=` check + `syscall.SIGTERM` from the current `QuitAgent`):

```go
//go:build !windows

package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Running reports whether the browser has its user data dir open. Chromium
// holds a SingletonLock symlink there, pointing at "<host>-<pid>". A browser
// that was killed can leave the link behind, so the PID must also be alive.
func (b Browser) Running() bool {
	target, err := os.Readlink(filepath.Join(b.Dir(), "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndex(target, "-")
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return true // unknown format: assume it runs, the safe answer
	}
	// Signal 0 checks existence; EPERM means it exists as another user.
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// detach puts the launched process in its own session, so it outlives the
// bx process that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// startBrowser opens the browser app normally (after SetDebugging).
func startBrowser(b Browser) error {
	bundle := b.App
	if i := strings.Index(bundle, ".app/"); i >= 0 {
		bundle = bundle[:i+len(".app")]
	}
	if out, err := exec.Command("open", "-a", bundle).CombinedOutput(); err != nil {
		return exit.New(exit.Upstream, "start %s: %v: %s", b.Label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// quitProcess asks the agent browser with this PID to quit (SIGTERM, which
// Chromium handles as a normal quit). It signals the PID only after
// confirming the process still runs with this agent's user data dir, so a
// reused PID is never touched.
func quitProcess(b Browser, pid int) error {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), "--user-data-dir="+b.Dir()) {
		return exit.New(exit.User, "%s is running but not as the process bx started; quit it from its window", b.Label)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return exit.New(exit.Upstream, "quit %s: %v", b.Label, err)
	}
	return nil
}
```

**New `internal/browser/proc_windows.go`** — `//go:build windows`:

```go
//go:build windows

package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Windows has no POSIX SingletonLock symlink and no syscall.Kill, so the
// process control here uses the built-in Windows tools (tasklist, taskkill)
// and PowerShell, the same way the macOS code uses open, osascript and ps.
// No cgo and no extra module: process creation flags come from the standard
// syscall package.
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// Running reports whether the browser is up. For the agent instance (its own
// data dir, started by bx launch) it checks the PID bx recorded; for the
// user's everyday browser it checks whether the executable runs at all, since
// several windows share one process and bx never started it.
func (b Browser) Running() bool {
	if b.Agent {
		raw, err := os.ReadFile(b.pidFile())
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			return false
		}
		return pidAlive(pid)
	}
	return exeRunning(filepath.Base(b.App))
}

// detach starts the process in its own group, detached from any console, so
// it outlives the bx process that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}

// startBrowser opens the browser app normally (after SetDebugging).
func startBrowser(b Browser) error {
	cmd := exec.Command(b.App)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return exit.New(exit.Upstream, "start %s: %v", b.Label, err)
	}
	_ = cmd.Process.Release()
	return nil
}

// quitProcess quits the agent browser with this PID. It first confirms the
// process still runs with this agent's user data dir, so a reused PID is
// never touched, then ends it and its child processes (renderers, GPU) with
// taskkill. Chromium's agent instance is bx's own throwaway profile, so a
// forced quit loses nothing the user cares about.
func quitProcess(b Browser, pid int) error {
	if !strings.Contains(procCmdline(pid), "--user-data-dir="+b.Dir()) {
		return exit.New(exit.User, "%s is running but not as the process bx started; quit it from its window", b.Label)
	}
	if out, err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput(); err != nil {
		return exit.New(exit.Upstream, "quit %s: %v: %s", b.Label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// exeRunning reports whether any process has the given executable name.
func exeRunning(name string) bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+name, "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(name))
}

// pidAlive reports whether a process with this PID is running.
func pidAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\""+strconv.Itoa(pid)+"\"")
}

// procCmdline returns a process's command line, or "" if it can't be read.
// The PID goes into a -Filter string, never the script body, and it is only
// ever an integer from bx's own PID file, so a page's text can't reach it.
func procCmdline(pid int) string {
	script := "(Get-CimInstance Win32_Process -Filter ('ProcessId=" + strconv.Itoa(pid) + "')).CommandLine"
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
```

## TASK 3 — daemon lock/detach seam (`internal/daemon`)

bx's `client.go` uses `syscall.Flock` (singleton lock so two starts don't race) and `syscall.SysProcAttr{Setsid}` (daemon spawn) — both POSIX-only.

**New `internal/daemon/lock_posix.go`** — `//go:build !windows`:

```go
//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// lockFile takes an exclusive, advisory, whole-file lock, released when the
// file is closed or the process exits, so two bx commands starting at once
// never start two daemons.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// detachDaemon puts the spawned daemon in its own session, so it outlives the
// bx command that started it.
func detachDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
```

**New `internal/daemon/lock_windows.go`** — `//go:build windows`:

```go
//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// Windows has no flock and no Setsid. The lock uses kernel32's LockFileEx,
// loaded lazily, so bx needs no extra module; the daemon is spawned detached
// with process-creation flags from the standard syscall package.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock = 0x00000002
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// lockFile takes an exclusive, whole-file lock, released when the file is
// closed or the process exits, so two bx commands starting at once never
// start two daemons.
func lockFile(f *os.File) error {
	var ol syscall.Overlapped
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 0xffffffff, 0xffffffff, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return e
	}
	return nil
}

// unlockFile releases the lock. A failed unlock is not fatal: the lock is
// released when the file is closed or the process exits anyway.
func unlockFile(f *os.File) error {
	var ol syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 0xffffffff, 0xffffffff, uintptr(unsafe.Pointer(&ol)))
	return nil
}

// detachDaemon starts the spawned daemon in its own group, detached from any
// console, so it outlives the bx command that started it.
func detachDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
```

## TASK 4 — Windows approval dialog

**New `internal/daemon/approve_windows.go`** — `//go:build windows`. Same security guarantees as macOS: separate process, default No, 60s timeout → No, message via env var (never the script body):

```go
//go:build windows

package daemon

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

// approve asks the user, in a Windows dialog outside the browser, to allow a
// change an agent asked for. The agent can't answer it: it is not part of
// any page bx can reach. No is the default button, and no answer within a
// minute counts as No. The message goes to the dialog through an environment
// variable, never the script text, so a page's text can't change it.
var approve = func(task, what string) error {
	msg := "An agent wants to " + what + "."
	if task != "" {
		msg = "The agent working on \"" + task + "\" wants to " + what + "."
	}
	const script = `Add-Type -AssemblyName System.Windows.Forms | Out-Null;` +
		`$r=[System.Windows.Forms.MessageBox]::Show($env:BX_APPROVE_MSG,'bx',` +
		`[System.Windows.Forms.MessageBoxButtons]::YesNo,` +
		`[System.Windows.Forms.MessageBoxIcon]::Warning,` +
		`[System.Windows.Forms.MessageBoxDefaultButton]::Button2);` +
		`if($r -eq [System.Windows.Forms.DialogResult]::Yes){exit 0}else{exit 1}`
	// A minute to answer, then the context ends powershell and it counts as No.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "BX_APPROVE_MSG="+msg)
	if err := cmd.Run(); err != nil {
		return exit.New(exit.Permission, "the user did not allow this: %s", what)
	}
	return nil
}
```

**Edit `internal/daemon/approve_other.go`** — change the build tag so Windows no longer uses the refusing stub:

```go
//go:build !darwin && !windows
```

> NOTE: this PR branches off `main`, where `approve` has signature `func(task, what string) error` (two args). The consent PR (#9) changes `approve` to three args / returns `remember`. Keep this `approve_windows.go` matching **main's** signature; whoever merges second rebases the other. Flag this in the PR description so Pramodh knows the two PRs touch `approve`.

## TASK 5 — rewire existing files to the seams

**`internal/browser/agent.go`:**
- Delete the whole `Running()` method; replace with a one-line comment: `// Running is defined per platform (proc_posix.go, proc_windows.go).`
- `Start()` body → `return startBrowser(b)`.
- In `LaunchAgent()`, replace `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}` with `detach(cmd)`.
- In `QuitAgent()`, replace the `ps -o command=` check + `syscall.Kill(pid, syscall.SIGTERM)` block with `if err := quitProcess(b, pid); err != nil { return false, err }` (keep the surrounding pidFile read, the `b.Running()` guard, and the wait loop).
- Remove the now-unused `"syscall"` import.

**`internal/daemon/client.go`:**
- In `startAndDial`, `syscall.Flock(int(lf.Fd()), syscall.LOCK_EX)` → `lockFile(lf)`, and `defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)` → `defer unlockFile(lf)`.
- In `spawn`, `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}` → `detachDaemon(cmd)`.
- Remove the now-unused `"syscall"` import.
- (`server.go` uses `syscall.ECONNREFUSED`, which exists on Windows — leave it.)

## TASK 6 — test isolation (so Windows CI is green)

bx's tests isolate with `t.Setenv("HOME", t.TempDir())`. On Windows, Go uses `%USERPROFILE%`/`%APPDATA%`/`%LOCALAPPDATA%`, so `HOME` does NOT redirect — those tests would read and WRITE real browser profiles and the real `%AppData%\bx`. Two fixes:

**6a. Guard the profile-writing browser tests to darwin.** In `internal/browser/browser_test.go`, add `import "runtime"` and at the top of `TestDetect` and `TestEnableDebugging`:

```go
	if runtime.GOOS != "darwin" {
		t.Skip("isolates data dirs via $HOME, which only redirects them on macOS; elsewhere this would touch real browser profiles")
	}
```

Make `TestTable` cross-platform (it currently asserts `/Applications/` and relative data dirs):

```go
func TestTable(t *testing.T) {
	if len(table) == 0 {
		t.Skip("no browser table for this OS")
	}
	seen := map[string]bool{}
	for _, b := range table {
		if seen[b.Name] || b.Label == "" || b.App == "" || !strings.HasSuffix(b.Inspect, "#remote-debugging") {
			t.Errorf("bad table entry %+v", b)
		}
		switch runtime.GOOS {
		case "darwin":
			if !strings.HasPrefix(b.App, "/Applications/") || strings.HasPrefix(b.DataDir, "/") {
				t.Errorf("darwin: bad paths %+v", b)
			}
		case "windows":
			if !filepath.IsAbs(b.DataDir) {
				t.Errorf("windows: DataDir must be absolute %+v", b)
			}
		}
		seen[b.Name] = true
	}
	if !seen[Default] {
		t.Fatal("Default is not in the table")
	}
}
```

**6b. Cross-platform config-home helper for the config-dir tests.** Some tests in `internal/app` and `internal/daemon` (e.g. `version_test.go`, `retention_test.go`) isolate the bx config dir with `t.Setenv("HOME", t.TempDir())`. On Windows that fails to redirect `os.UserConfigDir()` (`%AppData%`) and `os.UserHomeDir()` (`%USERPROFILE%`). Add a helper and use it in those tests:

```go
// in a shared test file per package, e.g. internal/daemon/testhome_test.go
// and internal/app/testhome_test.go

// testHome points HOME and the Windows home/config roots at a fresh temp dir,
// so a test's reads and writes never touch the real user's files on any OS.
func testHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)              // macOS/Linux
	t.Setenv("USERPROFILE", dir)       // Windows UserHomeDir
	t.Setenv("APPDATA", dir)           // Windows UserConfigDir
	t.Setenv("LOCALAPPDATA", dir)      // Windows local state
	return dir
}
```

Then replace `t.Setenv("HOME", t.TempDir())` with `testHome(t)` in the config-dir tests. **Run `go test ./...` on Windows and fix every remaining failure that is a real isolation leak** (grep the test files for `Setenv("HOME"` and `UserConfigDir`/`UserHomeDir`). Browser-profile tests that can't isolate on Windows (because `table` is built at init from `%LOCALAPPDATA%`) stay darwin-guarded per 6a — do NOT try to make those write to real dirs.

## TASK 7 — CI: run on Windows

**Edit `.github/workflows/ci.yml`** to a matrix so build+vet+test actually run on Windows:

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
jobs:
  test:
    strategy:
      matrix:
        os: [macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test ./...
```

The PR cannot merge until **both** legs are green. If a test can't be made to pass honestly on Windows, guard it (`runtime.GOOS`) with a clear skip reason — never weaken an assertion to force green.

## TASK 8 — docs

- **`README.md`** — the tagline/first line says "CLI and MCP server for macOS." Change to "for macOS and Windows." Add a short "Windows" note: supported browsers are the same; install for now via `go install github.com/Facets-cloud/bx@latest` (npm packaging is a follow-up); the approval prompt is a native Windows dialog. Keep it brief and honest.
- **`SECURITY.md`** — wherever it describes the approval dialog, add that on Windows the same guarantee holds via a native message box (outside the browser, the agent can't answer it, default No, 60-second timeout to No).
- **Permission-message polish (optional but on-brand):** `browser.go` and `agent.go` have permission-error strings hard-coded to macOS ("macOS blocked reading … give … Full Disk Access …"). On Windows these are misleading. Add a tiny platform string — `permHint` in `proc_darwin`/`proc_windows` (or reuse the proc files) — and use it in those messages, or wrap them in `runtime.GOOS == "darwin"`. Low priority; do it if time permits and note it either way.

## TASK 9 — Windows unit tests

Add `internal/browser/table_windows_test.go` (`//go:build windows`) covering the pure logic that has no macOS equivalent:
- `expandEnv`: `%LOCALAPPDATA%\x` expands; a bare `%` is left alone; unknown var → empty.
- `firstExisting`: returns the first existing path; falls back to the first candidate when none exist (point it at `t.TempDir()` files you create, plus a missing one).
- `winTable()`: five entries, all `DataDir` absolute, all `App` non-empty, names unique, `Default` ("brave") present.

These run on the `windows-latest` CI leg and give real Windows coverage without touching real browsers.

## VERIFICATION (run before committing)

```bash
# builds on all three, macOS behavior unchanged:
GOOS=windows go build ./...   && echo WIN_BUILD_OK
GOOS=darwin  go build ./...   && echo DARWIN_BUILD_OK
GOOS=linux   go build ./...   && echo LINUX_BUILD_OK
GOOS=windows go vet ./...     && GOOS=darwin go vet ./...
# test binaries compile for each target:
GOOS=windows go test -c -o /dev/null ./...   # (per package)
GOOS=darwin  go test -c -o /dev/null ./...
gofmt -l internal/ .          # must print nothing (ignore CRLF-only noise; check committed blobs with: git show HEAD:<f> | gofmt -d)
```

On a Windows machine or the `windows-latest` CI leg: `go test ./...` must be green. Be honest in the PR: CI covers build/vet/unit tests on Windows; the **interactive path** (the real MessageBox appearing; a live browser driven over CDP; AF_UNIX round-trip under a real daemon) still wants a manual spot-check on a Windows box — state that plainly, don't claim end-to-end live verification you didn't do.

## FINISH

```bash
gofmt -w internal/ .   # if needed
git add -A
git commit -m "feat(windows): build and run bx on Windows

Additive, build-tagged Windows support (Edge/Brave/Chrome/Vivaldi/Chromium):
browser + profile discovery under %LOCALAPPDATA%, process control via
tasklist/taskkill, daemon lock via LockFileEx, and a native approval dialog
with the same guarantees as macOS (outside the browser, default No, 60s
timeout). No new module dependency; macOS behavior is unchanged. Adds a
windows-latest CI leg. Distribution (npm/release artifacts) is a follow-up.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
git push -u origin feat/windows-support
```

Open the PR to `Facets-cloud/bx` with a body that: (1) lists what works on Windows, (2) says macOS is byte-for-byte unchanged and there's no new dependency, (3) points at the **approval dialog** as the security-critical file and explains its guarantees, (4) states exactly what the Windows CI leg covers vs the interactive spot-check still needed, (5) notes distribution is a deliberate follow-up, and (6) flags that it touches `approve` (coordinate the rebase with PR #9). Use `--draft` first so Arnav reviews before it goes to Pramodh.

## ANTI-DRIFT — definition of done

All 9 rows checked; `GOOS={windows,darwin,linux} go build` green; both CI legs (macOS + Windows) green; the PR body honest about interactive coverage; opened as a draft. Any item skipped (e.g. the permission-message polish) is stated in the PR, not left silently undone.
