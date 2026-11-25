package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/browser"
	"bxw/internal/contextmgr"
)

func TestC4RegionMemoizationGolden(t *testing.T) {
	// Build a page with nav, main, and footer landmarks:
	// e1 navigation "Site Nav" (depth 0)
	//   e2 link "Home" (depth 1)
	//   e3 link "About" (depth 1)
	// e4 main "Main Content" (depth 0)
	//   e5 heading "Welcome" (depth 1)
	//   e6 text "Intro text" (depth 1)
	// e7 contentinfo "Footer" (depth 0)
	//   e8 text "Copyright 2026" (depth 1)
	nodes := []SnapNode{
		{Ref: "e1", Depth: 0, Role: "navigation", Name: "Site Nav"},
		{Ref: "e2", Depth: 1, Role: "link", Name: "Home"},
		{Ref: "e3", Depth: 1, Role: "link", Name: "About"},
		{Ref: "e4", Depth: 0, Role: "main", Name: "Main Content"},
		{Ref: "e5", Depth: 1, Role: "heading", Name: "Welcome"},
		{Ref: "e6", Depth: 1, Role: "text", Name: "Intro text"},
		{Ref: "e7", Depth: 0, Role: "contentinfo", Name: "Footer"},
		{Ref: "e8", Depth: 1, Role: "text", Name: "Copyright 2026"},
	}

	// 1. First run with empty cache: everything is expanded, hashes generated
	outNodes1, memoized1, hashes1 := MemoizeRegions(nodes, nil)
	if len(outNodes1) != len(nodes) {
		t.Fatalf("first run should keep all nodes, got len %d", len(outNodes1))
	}
	if len(memoized1) != 0 {
		t.Fatalf("first run should have 0 memoized regions, got %d", len(memoized1))
	}
	if len(hashes1) != 3 {
		t.Fatalf("expected 3 region hashes (nav, main, contentinfo), got %d", len(hashes1))
	}

	// 2. Second run where only main changes:
	mutatedNodes := []SnapNode{
		{Ref: "e1", Depth: 0, Role: "navigation", Name: "Site Nav"},
		{Ref: "e2", Depth: 1, Role: "link", Name: "Home"},
		{Ref: "e3", Depth: 1, Role: "link", Name: "About"},
		{Ref: "e4", Depth: 0, Role: "main", Name: "Main Content"},
		{Ref: "e5", Depth: 1, Role: "heading", Name: "Updated Title"}, // changed!
		{Ref: "e6", Depth: 1, Role: "text", Name: "New text"},         // changed!
		{Ref: "e7", Depth: 0, Role: "contentinfo", Name: "Footer"},
		{Ref: "e8", Depth: 1, Role: "text", Name: "Copyright 2026"},
	}

	outNodes2, memoized2, hashes2 := MemoizeRegions(mutatedNodes, hashes1)

	// navigation (e1) and contentinfo (e7) should be memoized!
	if len(memoized2) != 2 {
		t.Fatalf("expected 2 memoized regions, got %d (%v)", len(memoized2), memoized2)
	}
	if _, ok := memoized2["e1"]; !ok {
		t.Errorf("expected e1 to be memoized")
	}
	if _, ok := memoized2["e7"]; !ok {
		t.Errorf("expected e7 to be memoized")
	}
	if _, ok := memoized2["e4"]; ok {
		t.Errorf("main (e4) was changed, should not be memoized")
	}

	// outNodes2 should have root e1, full e4 subtree (3 nodes), and root e7
	if len(outNodes2) != 5 {
		t.Fatalf("expected 5 nodes in transformed tree, got %d", len(outNodes2))
	}

	// 3. Check text rendering
	snap := Snapshot{
		Nodes:    outNodes2,
		Memoized: memoized2,
	}
	rendered := snap.Text()

	// e1 should format as `e1 navigation "Site Nav" unchanged (cache "<hash>")`
	navHash := hashes1["navigation:Site Nav"]
	wantNav := fmt.Sprintf(`e1 navigation "Site Nav" unchanged (cache %q)`, navHash)
	if !strings.Contains(rendered, wantNav) {
		t.Errorf("rendered snapshot missing nav cache reference %q; got:\n%s", wantNav, rendered)
	}

	// e7 footer should format as `e7 contentinfo "Footer" unchanged (cache "<hash>")`
	footerHash := hashes1["contentinfo:Footer"]
	wantFooter := fmt.Sprintf(`e7 contentinfo "Footer" unchanged (cache %q)`, footerHash)
	if !strings.Contains(rendered, wantFooter) {
		t.Errorf("rendered snapshot missing footer cache reference %q; got:\n%s", wantFooter, rendered)
	}

	// main should be expanded with children
	if !strings.Contains(rendered, `e5 heading "Updated Title"`) {
		t.Errorf("rendered snapshot missing expanded main child e5; got:\n%s", rendered)
	}

	// hashes2 should update main's hash while preserving others
	if hashes2["main:Main Content"] == hashes1["main:Main Content"] {
		t.Errorf("main hash should have updated")
	}
}

func TestC4RegionMemoizationIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live browser action test in short mode")
	}

	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no supported Chromium browser found on this machine")
	}

	targetBrowser := bs[0]

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>C4 Region Test</title></head>
<body>
  <nav aria-label="Global Nav">
    <a href="#home">Home</a>
    <a href="#docs">Docs</a>
  </nav>
  <main aria-label="Primary Content">
    <h1 id="headline">Initial Headline</h1>
    <p id="bodytext">Initial main body text paragraph.</p>
  </main>
  <footer aria-label="Site Footer">
    <p>Copyright 2026 Test Inc.</p>
  </footer>
</body>
</html>`))
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
			if tabID != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tabID == "" {
		t.Fatal("tab not found after launch")
	}

	// Reset default session in context store to start fresh
	_ = contextmgr.SaveSession(contextmgr.SessionModel{
		SessionID:    "default",
		RegionHashes: make(map[string]string),
	})

	// 1. Initial full snapshot: establishes region hashes
	snap1, err := d.snapshot(tabID, true, "", "", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("first snapshot failed: %v", err)
	}
	if len(snap1.Memoized) != 0 {
		t.Errorf("first snapshot should have 0 memoized regions, got %d", len(snap1.Memoized))
	}

	// 2. Mutate only the main section in the DOM
	sess, err := d.tabSession(tabID)
	if err != nil {
		t.Fatalf("tabSession: %v", err)
	}
	ctx := context.Background()
	err = sess.eval(ctx, `document.getElementById('headline').textContent = 'Brand New Headline'; true;`, nil)
	if err != nil {
		t.Fatalf("eval mutation: %v", err)
	}

	// 3. Second full snapshot: nav and footer should be memoized, main re-expands
	snap2, err := d.snapshot(tabID, true, "", "", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("second snapshot failed: %v", err)
	}

	rendered2 := snap2.Text()
	if len(snap2.Memoized) == 0 {
		t.Fatalf("expected memoized regions in second snapshot, got none:\n%s", rendered2)
	}

	if !strings.Contains(rendered2, "unchanged (cache ") {
		t.Errorf("rendered text should contain cache reference:\n%s", rendered2)
	}
	if !strings.Contains(rendered2, "Brand New Headline") {
		t.Errorf("rendered text should contain updated main headline:\n%s", rendered2)
	}
}
