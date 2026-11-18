# WORK ORDER — bx PR #9 (per-origin consent) guard-rails

**For:** implementation model (follow exactly; the design thinking is already done below).
**Repo:** `Facets-cloud/bx` via fork `arnavchaurasia-knp/bx`.
**Branch:** `feat/per-origin-consent` (the branch of PR #9 — you UPDATE it, do not open a new PR).
**PR:** https://github.com/Facets-cloud/bx/pull/9 (state: CHANGES_REQUESTED by pramodh-ayyappan).
**Identity for commits:** `git config user.name "arnavxchaurasia"`, `git config user.email "arnav.chaurasia@facets.cloud"`.

## Setup

```bash
git clone https://arnavchaurasia-knp@github.com/arnavchaurasia-knp/bx.git
cd bx
git checkout feat/per-origin-consent
git config user.name "arnavxchaurasia"
git config user.email "arnav.chaurasia@facets.cloud"
```

bx is a macOS tool. You are likely on Windows/Linux, so **verify with `GOOS=darwin go build ./...` and `GOOS=darwin go vet ./...`**, and run `go test ./internal/consent/` natively (that package is platform-neutral). The darwin runtime tests run on bx's macOS CI — say so honestly, don't claim you ran them.

## What the reviewer asked for (tracking table — keep this updated)

| # | Item | Status |
|---|------|--------|
| 1 | Scope a grant to **browser + profile + site** (not host only) | [x] |
| 2 | Dialog says what "Always allow" covers | [x] |
| 3 | Ship `bx consent` (list) + `bx consent forget <site>` (MCP optional) | [x] |
| 4 | Log dialog-skipped changes so `bx history` shows them | [x] |
| 5 | Don't remember an empty host (`file://`, `about:blank`) | [x] |
| 6 | Docs: SECURITY.md, README Safety, `StorageChange` comment, skill | [x] |
| 7 | Drop unused `ScopeSession` / session map / capability field | [x] |
| 8 | Lock `Remember`'s read-modify-write; stop dropping its error | [x] |
| 9 | Add a `consentFor` test (stub the `approve` var) | [x] |

---

## DESIGN DECISIONS (already resolved — do not re-litigate)

- **Grant key = `browser|profile|site`.** Drop the capability dimension entirely (bx gates only storage). `Grant` struct holds `Browser`, `Profile`, `Site`, `GrantedAt`.
- **Durable-only store.** Remove `ScopeSession`, `ScopeAlways`, and the in-memory `session` map. Every grant is durable in `consent.json`. (There is no "session" option; bx's model is ask-every-time OR always.)
- **Profile at the approval point:** in `changeStorage` (`internal/daemon/storagechange.go`) the browser is `s.b.Name`; the tab's profile is resolved the same way `audit.locate()` already does it — via `s.findTab(id)` → `info.BrowserContextID` → `s.profileOf[ctxID]`. Add a tiny helper `s.profileForTab(id) string` next to `locate` in `audit.go` and reuse it.
- **Empty host:** if `site == ""` (what `host()` returns for `file://`/`about:blank`), still ASK every time, and NEVER persist a grant.
- **History marker:** add `Auto bool` to the `Changed` result and to the audit `Entry`; `server.audit` already type-asserts the result (`res.(Tab)`), so add `res.(Changed)`. `actionLine` appends ` (saved choice)`.
- **`bx consent` reads/writes `consent.json` directly** via `consent.New(daemon.Dir())` — no daemon needed, so it works when no browser is running.
- **Locking:** hold the store's `sync.Mutex` across the whole load→modify→write in `Remember` and `Forget`, and RETURN their errors (no `_ =`).

---

## TASK 1 — Rewrite `internal/consent/consent.go` (items 1, 5, 7, 8)

Replace the file with this (the scope is now browser+profile+site, durable-only, locked):

```go
// Package consent remembers which (browser, profile, site) an agent may
// change storage on, so bx asks once instead of on every change. A grant is
// scoped to one browser, one profile and one site (the host shown in the
// approval dialog). Grants are written to <dir>/consent.json and outlast the
// daemon. Nothing a page can reach writes here, so a site can't grant itself;
// only a click on "Always allow" in the approval dialog does.
package consent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Grant is one remembered allowance: this browser+profile may change storage
// on this site, from GrantedAt, without asking again.
type Grant struct {
	Browser   string    `json:"browser"`
	Profile   string    `json:"profile"`
	Site      string    `json:"site"`
	GrantedAt time.Time `json:"granted_at"`
}

// Store holds remembered grants in <dir>/consent.json. The zero Store is not
// usable: call New. A Store is safe for concurrent use within one process.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New returns a store backed by <dir>/consent.json.
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) file() string { return filepath.Join(s.dir, "consent.json") }

// Remembered reports whether this browser+profile+site already has a grant.
// An empty site is never remembered.
func (s *Store) Remembered(browser, profile, site string) bool {
	if site == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.load() {
		if g.Browser == browser && g.Profile == profile && g.Site == site {
			return true
		}
	}
	return false
}

// Remember records a grant. An empty site is refused (never persisted).
// It holds the lock across the read-modify-write so two clicks can't race.
func (s *Store) Remember(browser, profile, site string) error {
	if site == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := s.load()
	for i, g := range grants {
		if g.Browser == browser && g.Profile == profile && g.Site == site {
			grants[i].GrantedAt = time.Now().UTC()
			return s.write(grants)
		}
	}
	grants = append(grants, Grant{Browser: browser, Profile: profile, Site: site, GrantedAt: time.Now().UTC()})
	return s.write(grants)
}

// Forget drops every grant for site (across browsers and profiles). It
// reports how many it removed.
func (s *Store) Forget(site string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := s.load()
	kept := grants[:0:0]
	removed := 0
	for _, g := range grants {
		if g.Site == site {
			removed++
			continue
		}
		kept = append(kept, g)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, s.write(kept)
}

// List returns every grant, sorted by site then profile.
func (s *Store) List() []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.load()
	// stable, human order
	sortGrants(g)
	return g
}

// load reads the grants. A missing or unreadable file is no grants: the
// dialog then asks, which is the safe default. Caller holds mu.
func (s *Store) load() []Grant {
	data, err := os.ReadFile(s.file())
	if err != nil {
		return nil
	}
	var grants []Grant
	if json.Unmarshal(data, &grants) != nil {
		return nil
	}
	return grants
}

// write replaces the grants file (0600 under the 0700 dir). Caller holds mu.
func (s *Store) write(grants []Grant) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(grants, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.file(), data, 0o600)
}

func sortGrants(g []Grant) {
	for i := 1; i < len(g); i++ {
		for j := i; j > 0 && less(g[j], g[j-1]); j-- {
			g[j], g[j-1] = g[j-1], g[j]
		}
	}
}

func less(a, b Grant) bool {
	if a.Site != b.Site {
		return a.Site < b.Site
	}
	if a.Browser != b.Browser {
		return a.Browser < b.Browser
	}
	return a.Profile < b.Profile
}
```

(If you prefer `sort.Slice`, use it and import `sort`; the hand-rolled sort just avoids an import.)

## TASK 2 — Rewrite `internal/consent/consent_test.go` (item 7/8 coverage)

Cover: empty site never remembered; grant is durable and scoped (different profile = not remembered); Forget by site; re-grant replaces. Keep it platform-neutral (uses `t.TempDir()`), so it runs on any OS.

```go
package consent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopedToBrowserProfileSite(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Remember("edge", "Personal", "github.com"); err != nil {
		t.Fatal(err)
	}
	if !New(dir).Remembered("edge", "Personal", "github.com") {
		t.Fatal("grant did not survive a restart")
	}
	// Same site, different profile = not covered.
	if New(dir).Remembered("edge", "Throwaway", "github.com") {
		t.Fatal("grant leaked across profiles")
	}
	// Same site, different browser = not covered.
	if New(dir).Remembered("brave", "Personal", "github.com") {
		t.Fatal("grant leaked across browsers")
	}
}

func TestEmptyHostNeverRemembered(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Remember("edge", "Personal", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "consent.json")); !os.IsNotExist(err) {
		t.Fatal("empty host wrote a grant")
	}
	if s.Remembered("edge", "Personal", "") {
		t.Fatal("empty host reported remembered")
	}
}

func TestForgetBySite(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Remember("edge", "Personal", "a.com")
	_ = s.Remember("brave", "Work", "a.com")
	_ = s.Remember("edge", "Personal", "b.com")
	n, err := s.Forget("a.com")
	if err != nil || n != 2 {
		t.Fatalf("Forget a.com = %d, %v; want 2", n, err)
	}
	if New(dir).Remembered("edge", "Personal", "a.com") {
		t.Fatal("a.com still remembered")
	}
	if !New(dir).Remembered("edge", "Personal", "b.com") {
		t.Fatal("Forget a.com wrongly dropped b.com")
	}
}

func TestRememberReplaces(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Remember("edge", "Personal", "s.com")
	_ = s.Remember("edge", "Personal", "s.com")
	if n := len(New(dir).List()); n != 1 {
		t.Fatalf("re-grant made %d records, want 1", n)
	}
}
```

## TASK 3 — Approval dialog text + remember signal (item 2)

`internal/daemon/approve_darwin.go` — the dialog must name the profile and say what Always allow covers. Change `approve`'s signature to take the site and profile for the message, OR keep it simple: pass the fully-built message in. Keep the 3 buttons and `clicked()` as they are. Minimal change: build the "Always allow" meaning into the dialog text the CALLER passes.

Decision: keep `approve(task, what string) (remember bool, err error)` unchanged, and make the CALLER (`consentFor`) pass a `what` that, for the dialog, already reads well. Then add a SECOND line to the osascript message describing the Always-allow scope. Simplest: change `approve` to take one more string `alsoAllows` and append it:

```go
var approve = func(task, what, alsoAllows string) (remember bool, err error) {
	msg := "An agent wants to " + what + "."
	if task != "" {
		msg = "The agent working on \"" + task + "\" wants to " + what + "."
	}
	if alsoAllows != "" {
		msg += "\n\n" + alsoAllows
	}
	// ... unchanged osascript call, buttons {"Deny","Allow","Always allow"},
	//     default button "Deny" ...
}
```

Update `approve_other.go` and (if PR #3's branch ever merges) any other `approve` to the new 3-arg signature. `alsoAllows` example string (built in `consentFor`):

```
Always allow: let agents change any cookie or storage on github.com in your Personal profile without asking.
```

## TASK 4 — Wire scope + remember-signal + empty-host into `consentFor` and `changeStorage` (items 1, 4, 5)

`internal/daemon/consent.go` (the daemon wrapper) — rewrite `consentFor`:

```go
// consentFor allows a storage change on a site in a given browser+profile,
// asking the user only when that browser+profile+site is not already
// remembered. "Always allow" persists the grant. It returns whether the
// change was allowed by a saved grant (no dialog), so the caller can mark the
// audit entry. An empty site is always asked and never remembered.
func consentFor(task, what, browser, profile, site string) (auto bool, err error) {
	store := sites()
	if store.Remembered(browser, profile, site) {
		return true, nil // allowed by a saved choice; no dialog
	}
	alsoAllows := ""
	if site != "" {
		prof := profile
		if prof == "" {
			prof = "this"
		}
		alsoAllows = "Always allow: let agents change any cookie or storage on " + site +
			" in your " + prof + " profile without asking."
	}
	remember, err := approve(task, what, alsoAllows)
	if err != nil {
		return false, err
	}
	if remember {
		_ = store.Remember(browser, profile, site) // no-op for empty site
	}
	return false, nil
}
```

Remove the old `capStorage` const and any capability plumbing.

`internal/daemon/storagechange.go` — in `changeStorage`, resolve browser+profile and thread the `auto` flag into the result:

```go
	site := host(page)
	what := c.describe(site)
	task, _ := sess.title.Load().(string)
	profile := s.profileForTab(id) // new helper, see TASK 5
	auto, err := consentFor(task, what, s.b.Name, profile, site)
	if err != nil {
		return Changed{}, err
	}
	// ... perform the change ...
	return Changed{Changed: what, Auto: auto}, nil
```

Add the field to the result struct (same file):

```go
type Changed struct {
	Changed string `json:"changed"`
	Auto    bool   `json:"auto,omitempty"` // allowed by a saved grant, no dialog
}
```

Update the `StorageChange` doc comment (item 6) to say the change waits for Allow **unless the site is already remembered for this browser and profile**.

## TASK 5 — `profileForTab` helper + audit marker (item 4)

`internal/daemon/audit.go` — add a helper next to `locate` that returns a tab's profile dir (reuse the existing resolution):

```go
// profileForTab is the profile folder of the tab, or "" if unknown.
func (s *server) profileForTab(id string) string {
	info, err := s.findTab(id)
	if err != nil {
		return ""
	}
	return s.tabInfo(info, s.profileNames()).Profile
}
```

Add `Auto` to the audit `Entry` struct:

```go
	Auto bool `json:"auto,omitempty"` // the change skipped the dialog via a saved grant
```

In `server.audit`, after the existing `if tab, ok := res.(Tab); ok { ... }` block, add:

```go
	if ch, ok := res.(Changed); ok && ch.Auto {
		e.Auto = true
	}
```

`internal/app/history.go` — in `actionLine`, append the marker when set:

```go
func actionLine(e daemon.Entry) string {
	s := e.T.Local().Format("15:04:05") + "  " + strings.TrimSpace(e.Op+" "+e.Target)
	if e.Auto {
		s += "  (saved choice)"
	}
	// ... rest unchanged
}
```

## TASK 6 — `bx consent` and `bx consent forget <site>` (item 3)

New file `internal/app/consentcmds.go`:

```go
package app

import (
	"github.com/Facets-cloud/bx/internal/consent"
	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// cmdConsent lists the sites the user has told bx to always allow storage
// changes on, by browser and profile.
func cmdConsent(e env) error {
	if err := need(e.pos, 0, "bx consent"); err != nil {
		return err
	}
	dir, err := daemon.Dir()
	if err != nil {
		return err
	}
	grants := consent.New(dir).List()
	if e.o.json {
		e.o.printJSON(grants)
		return nil
	}
	if len(grants) == 0 {
		e.o.println("no saved storage-change allowances. bx asks before every change.")
		return nil
	}
	var rows [][]string
	for _, g := range grants {
		rows = append(rows, []string{g.Browser, g.Profile, g.Site, g.GrantedAt.Local().Format("2006-01-02 15:04")})
	}
	e.o.table([]string{"BROWSER", "PROFILE", "SITE", "SINCE"}, rows)
	return nil
}

// cmdConsentForget drops every saved allowance for a site.
func cmdConsentForget(e env) error {
	if err := need(e.pos, 1, "bx consent forget <site>"); err != nil {
		return err
	}
	dir, err := daemon.Dir()
	if err != nil {
		return err
	}
	n, err := consent.New(dir).Forget(e.pos[0])
	if err != nil {
		return err
	}
	if n == 0 {
		return exit.New(exit.User, "no saved allowance for %q", e.pos[0])
	}
	e.o.printf("forgot %d allowance(s) for %s; bx will ask again.\n", n, e.pos[0])
	return nil
}
```

Register in `internal/app/app.go`'s `commands` map (dispatch matches the longest key, so register both):

```go
	"consent":        cmdConsent,
	"consent forget": cmdConsentForget,
```

> **Check the `env`/`out` API names** in `cmds.go`/`output.go` before writing: confirm the exact spellings of `need`, `e.pos`, `e.o.json`, `e.o.table`, `e.o.printJSON`, `e.o.println`, `e.o.printf`. Match whatever is actually there (e.g. it may be `e.o.print`/`fmt.Fprintf(e.o.w, ...)`). Do not invent methods.

Add `bx consent` to the help/usage text wherever other commands are listed (grep for `"config"` in the usage strings).

**MCP (optional, "if easy" per the review):** add a read-only `consent_list` tool in `internal/app/mcp.go` mirroring `cmdConsent` (one `mcpTool` entry forwarding to the same listing). Skip `forget` over MCP (a mutation an agent shouldn't self-serve). If the `mcpTool` wiring is non-trivial, leave MCP out and say so in the PR comment — the CLI is the required part.

## TASK 7 — Docs (item 6)

- **`SECURITY.md`** — find the line that says you allow each storage change (e.g. "You allow each storage change"). Add that a change can be remembered per browser+profile+site via "Always allow", that remembered changes are logged (`bx history` shows "(saved choice)"), and that `bx consent` lists them and `bx consent forget <site>` revokes.
- **`README.md`** — the Safety section: same two sentences, plus the `bx consent` commands.
- **`internal/daemon/storagechange.go`** — the `StorageChange` type comment (done in TASK 4).
- **The skill** — grep for the skill text (search `SKILL`, `skill`, `plugin/embed`, `*.md` under `plugin/`). Wherever it describes the storage-change approval, add one line about remembering per browser+profile+site and `bx consent`.

## VERIFICATION (run before committing)

```bash
go test ./internal/consent/            # must pass on any OS
GOOS=darwin go build ./...             # bx's real target
GOOS=darwin go vet ./...
GOOS=darwin go test -c -o /dev/null ./internal/daemon/   # daemon tests compile for darwin
gofmt -l internal/consent internal/daemon internal/app   # must print nothing
```

Add a `consentFor` test (item 9) in `internal/daemon/consent_test.go` — `approve` is a package var, so stub it:

```go
//go:build darwin
package daemon

import "testing"

func TestConsentFor(t *testing.T) {
	orig := approve
	defer func() { approve = orig }()

	// Deny -> error, nothing remembered.
	approve = func(task, what, also string) (bool, error) { return false, assertDenied() }
	// ... set a temp Dir(), call consentFor, assert err != nil ...

	// Allow (no remember) -> auto=false, err=nil.
	// Always allow -> store.Remember called; a second consentFor returns auto=true WITHOUT calling approve.
}
```

(Keep it darwin-tagged since the daemon package only builds on darwin/the supported OS. Use `daemon.Dir()`'s dir via `t.Setenv` of the config-dir env if needed, or point the store dir at `t.TempDir()` by factoring `sites()` to honor an override — simplest: test the `consent.Store` directly for the remember/auto logic and keep `consentFor` coverage light.)

## FINISH

```bash
gofmt -w internal/consent internal/daemon internal/app   # if gofmt -l printed anything
git add -A
git commit -m "feat(consent): scope grants to browser+profile+site, add bx consent, log skips

Addresses review on #9: per-profile scope, dialog explains Always allow,
bx consent list/forget, audit shows saved-choice skips, empty-host guard,
locked read-modify-write, docs + consentFor test.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
git push origin feat/per-origin-consent
```

Then comment on PR #9 mapping each review point to what changed, and note what runs on macOS CI vs what you verified by cross-build. Do NOT mark anything you did not run as "tested"; say "built and vetted for darwin; consent store tested natively; daemon runtime tests run on CI".

## ANTI-DRIFT — definition of done

All 9 rows in the tracking table checked, the five VERIFICATION commands green, the PR branch pushed, and a review-point-by-point comment posted on #9. If any item is skipped (e.g. MCP), say so explicitly in the PR comment rather than leaving it silently undone.
