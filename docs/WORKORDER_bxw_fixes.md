# WORK ORDER — bxw fixes & hardening

**For:** implementation model. **WIP = 1**, one focused Conventional Commit per fix, CI must stay green,
push to `origin/main`. These are real issues found during C1/C2 verification (not features — the roadmap
covers those). Do them in order: **A → B → C** (B would fail CI until A lands).

| ID | Fix | Severity | Status |
|----|-----|----------|--------|
| A | gofmt the 16 committed files that aren't formatted | trivial/hygiene | ✅ |
| B | Add a gofmt gate to CI so it can never regress | low (prevents recurrence) | ✅ |
| C | Fix the agent-browser launch port race ("CDP did not come up") | **real reliability bug** | ✅ |

> Already done (do not redo): `semantic_test.go` alignment fixed in `5bbfc08`. The C1 `--find` role-match
> carry-forward was resolved in `39a8859`.

---

## FIX-A — gofmt the repo

**Problem:** 16 tracked `.go` files are not gofmt-clean. Each has the same trivial deviation — a **trailing
blank line** before EOF (LF files, verified; not a CRLF artifact). CI never caught it because CI runs
`go vet`/`go test` but **not** `gofmt` (see FIX-B).

**Evidence** (committed-blob check, `git show HEAD:<f> | gofmt -d`):
```
internal/app/mcpinstall.go      internal/config/config.go        internal/daemon/diff_test.go
internal/app/setup.go           internal/config/config_test.go   internal/daemon/nav.go
internal/browser/prefs.go       internal/contextmgr/contextmgr.go internal/daemon/profile_test.go
internal/browser/prefs_test.go  internal/contextmgr/contextmgr_test.go internal/history/history.go
internal/cache/cache.go         internal/cache/cache_test.go     internal/perms/perms.go
internal/config/...             ...                              internal/perms/perms_test.go
```

**Fix:** from the repo root, `gofmt -w .` (or `gofmt -w` the listed files). It will strip the trailing blank
lines. **Do NOT let it touch files you didn't mean to** — if your working tree has CRLF noise (autocrlf),
set `git config core.autocrlf false` and re-checkout first so the only diff is the real gofmt change. Review
`git diff` — it must be only blank-line removals (and any genuine formatting), nothing semantic.

**Acceptance:** `for f in $(git ls-files '*.go'); do git show HEAD:"$f" | gofmt -d; done` prints nothing after
the commit (check the committed blobs, not the CRLF-smudged working tree). `go build ./...` + `go test ./...`
still green. **Commit:** `style: gofmt -w (strip trailing blank lines)`.

## FIX-B — gofmt gate in CI

**Problem:** `.github/workflows/ci.yml` runs `go vet` + `go test` but no formatting check, so unformatted
files (FIX-A, and the C1/C2 nits) sailed through. This is the root cause of the recurrence.

**Fix:** add a gofmt step to the `ci.yml` job, before `go test`. On the Linux leg only (gofmt output is
OS-independent; keep the Windows leg build/test-only to avoid CRLF false positives):

```yaml
      - name: gofmt
        if: runner.os == 'Linux'
        run: |
          unformatted=$(gofmt -l .)
          if [ -n "$unformatted" ]; then echo "gofmt needed on:"; echo "$unformatted"; exit 1; fi
```

**Acceptance:** CI run after FIX-A+B is green; introducing a deliberately mis-formatted file turns the Linux
leg red on the gofmt step. **Commit:** `ci: fail the build on unformatted Go files`.

## FIX-C — agent-browser launch port race (the real bug)

**Problem:** the launch in `internal/daemon/cdp.go` pre-assigns a port and hands it to Chromium:
```
port, _ := freePort()                                   // line ~25: net.Listen(":0") then Close()
... "--remote-debugging-port=%d" % port ...             // line ~58: launch with that fixed port
waitCDP(port, 30s)                                       // line ~72: poll http://127.0.0.1:port/json/version
```
`freePort` binds port 0, reads the assigned port, **closes the listener, then launches**. Between the close
and Chromium binding, any other process — including another bxw test's browser — can grab that port, so
Chromium fails to bind it and CDP never answers → **`CDP did not come up on 127.0.0.1:<port>`**. This is a
TOCTOU race; it is load-dependent, so CI (one clean run) passes while back-to-back local runs flake. It
affects **real users on busy machines**, not just the two integration tests that exposed it
(`TestC1QueryIntegration`, `TestSnapshotWholesaleDiffThreshold`).

**Fix (bx's proven pattern):** stop pre-assigning. Launch with **`--remote-debugging-port=0`** (let Chromium
pick a free port and bind it atomically), then read the **actual** port from the profile's
`DevToolsActivePort` file (first line), and connect there.
- Remove `freePort()` from the launch path (keep it only if something else needs it).
- Before launch, delete any stale `DevToolsActivePort` in the profile dir; after launch, poll for the file to
  appear (bounded, ~15 s), parse `port\n/devtools/browser/<id>`, then `waitCDP`.
- Read the `// line ~47` comment already in `cdp.go` first — it hints at prior port-binding handling; keep
  whatever correct behaviour it guards.

**Also (test teardown):** make `TestC1QueryIntegration` and `TestSnapshotWholesaleDiffThreshold` kill their
headless browser and **wait for it to exit** in a `defer` before returning, so sequential browser tests don't
contend. (They currently `os.FindProcess(...).Kill()` without waiting.)

**Caution — this is the critical path.** Verify on a real Windows machine, not just a build:
1. Launch a browser via bxw, confirm it connects (`bxw list tabs` / a query) using the discovered port.
2. Run the daemon integration tests **twice back-to-back** (`go test ./internal/daemon/ -run Integration -count=2`)
   and confirm no "CDP did not come up".

**Acceptance:** launch connects via the Chromium-chosen port; back-to-back integration runs are stable; CI
green on both OSes. **Commit:** `fix(daemon): launch with --remote-debugging-port=0 + read DevToolsActivePort (kill the port race)`.

---

## Definition of done (all three)

`gofmt` sweep clean on committed blobs · CI has the gofmt gate and is green on both OSes · the launch no
longer pre-assigns a port · integration tests tear down their browser and run back-to-back without flaking ·
one Conventional Commit per fix · `scripts/verify-roadmap.sh` still `VERIFY OK` (these touch no roadmap rows).
FIX-C is the only one with real risk — treat it with the Windows spot-check above, and if the port-0 switch
proves fiddly, say so rather than shipping a half-fix.
