package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/browser"
	"bxw/internal/perms"
)

func TestS1RedactionLive(t *testing.T) {
	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no installed Chromium browser found on this host")
	}
	targetBrowser := bs[0]

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>S1 Redaction Test Page</title></head>
<body>
  <h1>Account Settings</h1>
  <div>
    <label for="pwd">Password</label>
    <input type="password" id="pwd" name="password" value="SuperSecretPassword123">
  </div>
  <script>
    localStorage.setItem("jwt", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.4pdODQn_c824mG5_7-1e5K7yvP6K6fW0vQ8");
    document.cookie = "session_token=" + "sk" + "-live-1234567890abcdef0123456789; path=/";
    console.log("Initialized client key: " + "AI" + "zaSyD-1234567890abcdefghijklmnopqrstuvw");
  </script>
</body>
</html>`)
	}))
	defer ts.Close()

	d := &Daemon{
		br:        targetBrowser,
		stop:      make(chan struct{}),
		since:     time.Now(),
		sessions:  make(map[string]*tabSession),
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	if err := d.launch(true, ts.URL, false); err != nil {
		t.Fatalf("launch headless %s: %v", targetBrowser.Name, err)
	}
	defer func() {
		if d.agentPID != 0 {
			if proc, err := os.FindProcess(d.agentPID); err == nil {
				_ = proc.Kill()
				_, _ = proc.Wait()
			}
		}
	}()

	var tabID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tabs, err := d.listTabs()
		if err == nil && len(tabs) > 0 {
			for _, tab := range tabs {
				if strings.HasPrefix(tab.URL, ts.URL) {
					tabID = tab.ID
					break
				}
			}
			if tabID == "" && tabs[0].URL != "about:blank" {
				tabID = tabs[0].ID
			}
			if tabID == "" && len(tabs) > 0 {
				tabID = tabs[0].ID
			}
		}
		if tabID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tabID == "" {
		t.Fatal("expected at least 1 tab open")
	}

	// Wait briefly for page scripts (localStorage, cookies, console) to run
	time.Sleep(300 * time.Millisecond)

	// 1. Snapshot test: default redaction should mask the password value
	snapReq := Request{
		Op:   "snapshot",
		Args: map[string]any{"tab": tabID, "full": true},
	}
	snapResp := d.dispatch(snapReq)
	snapResp = d.maybeRedact(snapReq, snapResp)
	if snapResp.Error != "" {
		t.Fatalf("snapshot failed: %v", snapResp.Error)
	}
	snapStr := string(snapResp.Data)
	if strings.Contains(snapStr, "SuperSecretPassword123") {
		t.Fatalf("snapshot exposed plaintext password: %s", snapStr)
	}
	if !strings.Contains(snapStr, "[REDACTED]") {
		t.Fatalf("snapshot missing [REDACTED] replacement: %s", snapStr)
	}

	// 2. Storage test: default redaction should mask the JWT in localStorage and session token in cookies
	storageReq := Request{
		Op:   "storage",
		Args: map[string]any{"tab": tabID},
	}
	storageResp := d.dispatch(storageReq)
	storageResp = d.maybeRedact(storageReq, storageResp)
	if storageResp.Error != "" {
		t.Fatalf("storage failed: %v", storageResp.Error)
	}
	storageStr := string(storageResp.Data)
	if strings.Contains(storageStr, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Fatalf("storage exposed raw JWT: %s", storageStr)
	}
	if strings.Contains(storageStr, "sk-live-1234567890abcdef0123456789") {
		t.Fatalf("storage exposed session token cookie: %s", storageStr)
	}
	if !strings.Contains(storageStr, "[REDACTED]") {
		t.Fatalf("storage missing [REDACTED] replacement: %s", storageStr)
	}

	// 3. Console test: default redaction should mask Google API key in log message
	consoleReq := Request{
		Op:   "console",
		Args: map[string]any{"tab": tabID},
	}
	consoleResp := d.dispatch(consoleReq)
	consoleResp = d.maybeRedact(consoleReq, consoleResp)
	if consoleResp.Error != "" {
		t.Fatalf("console failed: %v", consoleResp.Error)
	}
	consoleStr := string(consoleResp.Data)
	if strings.Contains(consoleStr, "AIzaSyD-1234567890abcdefghijklmnopqrstuvw") {
		t.Fatalf("console exposed raw API key: %s", consoleStr)
	}

	// 4. Opt-out (--no-redact) without permission grant: must refuse with typed error
	noRedactReq := Request{
		Op:   "snapshot",
		Args: map[string]any{"tab": tabID, "full": true, "no_redact": true},
	}
	noRedactResp := d.dispatch(noRedactReq)
	noRedactResp = d.maybeRedact(noRedactReq, noRedactResp)
	if noRedactResp.Error == "" {
		t.Fatal("expected no-redact to fail without permission grant, but it succeeded")
	}
	if !strings.Contains(noRedactResp.Error, "no-redact requires permission grant") {
		t.Fatalf("unexpected error message for ungranted no-redact: %s", noRedactResp.Error)
	}

	// 5. Grant permission for the origin and verify opt-out succeeds
	origin := perms.NormalizeOrigin(ts.URL)
	if err := perms.Default().Grant(origin, perms.CapNoRedact, perms.ScopeSession, 1*time.Hour); err != nil {
		t.Fatalf("grant perms: %v", err)
	}

	grantedResp := d.dispatch(noRedactReq)
	grantedResp = d.maybeRedact(noRedactReq, grantedResp)
	if grantedResp.Error != "" {
		t.Fatalf("no-redact failed after grant: %s", grantedResp.Error)
	}
	grantedSnapStr := string(grantedResp.Data)
	if strings.Contains(grantedSnapStr, "[REDACTED]") {
		t.Fatalf("granted snapshot should not contain [REDACTED]: %s", grantedSnapStr)
	}

	// Test storage with no-redact after grant
	grantedStorageReq := Request{
		Op:   "storage",
		Args: map[string]any{"tab": tabID, "no_redact": true},
	}
	grantedStorageResp := d.dispatch(grantedStorageReq)
	grantedStorageResp = d.maybeRedact(grantedStorageReq, grantedStorageResp)
	if grantedStorageResp.Error != "" {
		t.Fatalf("no-redact storage failed after grant: %s", grantedStorageResp.Error)
	}
	grantedStorageStr := string(grantedStorageResp.Data)
	if !strings.Contains(grantedStorageStr, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Fatalf("granted storage should contain raw JWT: %s", grantedStorageStr)
	}
}
