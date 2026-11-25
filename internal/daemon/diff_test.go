package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/browser"
)

func TestDiffSnapshots(t *testing.T) {
	base := []SnapNode{
		{Ref: "e1", Role: "heading", Name: "Title"},
		{Ref: "e2", Role: "textbox", Name: "Email", Value: ""},
		{Ref: "e3", Role: "button", Name: "Submit"},
	}

	// 1. Unchanged
	diff, changed := DiffSnapshots(base, base)
	if changed != 0 || diff != "unchanged\n" {
		t.Errorf("expected unchanged, got changed=%d diff=%s", changed, diff)
	}

	// 2. Value modified
	modified := []SnapNode{
		{Ref: "e1", Role: "heading", Name: "Title"},
		{Ref: "e2", Role: "textbox", Name: "Email", Value: "alice@example.com"},
		{Ref: "e3", Role: "button", Name: "Submit"},
	}
	diff, changed = DiffSnapshots(base, modified)
	if changed != 1 || !strings.Contains(diff, "~ e2 textbox") {
		t.Errorf("expected 1 modified node, got %d (%s)", changed, diff)
	}

	// 3. Node added & removed
	mutated := []SnapNode{
		{Ref: "e1", Role: "heading", Name: "Title"},
		{Ref: "e4", Role: "status", Name: "Loading"},
	}
	diff, changed = DiffSnapshots(base, mutated)
	if changed != 3 { // e2 removed, e3 removed, e4 added
		t.Errorf("expected 3 changes, got %d (%s)", changed, diff)
	}
}

func TestFilterSubtreeAndFind(t *testing.T) {
	nodes := []SnapNode{
		{Ref: "e1", Depth: 0, Role: "form", Name: "Login"},
		{Ref: "e2", Depth: 1, Role: "textbox", Name: "Username"},
		{Ref: "e3", Depth: 1, Role: "textbox", Name: "Password"},
		{Ref: "e4", Depth: 0, Role: "footer", Name: "Copyright"},
	}

	subtree := FilterSubtree(nodes, "e1")
	if len(subtree) != 3 {
		t.Errorf("expected 3 nodes in e1 subtree, got %d", len(subtree))
	}

	found := FilterFind(nodes, "password")
	if len(found) != 1 || found[0].Ref != "e3" {
		t.Errorf("expected finding e3, got %+v", found)
	}

	// Carry-forward test: match role as well
	foundRole := FilterFind(nodes, "footer")
	if len(foundRole) != 1 || foundRole[0].Ref != "e4" {
		t.Errorf("expected finding e4 by role 'footer', got %+v", foundRole)
	}
}

func TestSnapshotPageReplacedFormatting(t *testing.T) {
	snap := Snapshot{
		Title:        "New Page",
		URL:          "https://example.com/new",
		PageReplaced: true,
		Nodes: []SnapNode{
			{Ref: "e1", Role: "heading", Name: "Welcome"},
		},
	}
	text := snap.Text()
	if !strings.HasPrefix(text, "[page replaced — full snapshot]\n") {
		t.Errorf("expected page replaced note prefix, got: %s", text)
	}
}

// TestSnapshotWholesaleDiffThresholdsPinned pins the F5 threshold constants so changes are conscious.
func TestSnapshotWholesaleDiffThresholdsPinned(t *testing.T) {
	if WholesaleThresholdChangedRatio != 0.70 {
		t.Errorf("WholesaleThresholdChangedRatio = %f, want 0.70", WholesaleThresholdChangedRatio)
	}
	if WholesaleThresholdTextLenRatio != 0.85 {
		t.Errorf("WholesaleThresholdTextLenRatio = %f, want 0.85", WholesaleThresholdTextLenRatio)
	}
}

func TestSnapshotWholesaleDiffThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no supported browser found")
	}
	b := bs[0]

	d := &Daemon{
		br:        b,
		stop:      make(chan struct{}),
		since:     time.Now(),
		sessions:  make(map[string]*tabSession),
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	initialHTML := "data:text/html,<!DOCTYPE html><html><body><ul>" +
		"<li>Item 1</li><li>Item 2</li><li>Item 3</li><li>Item 4</li><li>Item 5</li>" +
		"<li>Item 6</li><li>Item 7</li><li>Item 8</li><li>Item 9</li><li>Item 10</li>" +
		"</ul></body></html>"

	if err := d.launch(true, initialHTML, false); err != nil {
		t.Fatalf("launch headless: %v", err)
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
			tabID = tabs[0].ID
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tabID == "" {
		t.Fatal("expected at least 1 tab open")
	}

	// 1. Initial snapshot to establish baseline
	baseSnap, err := d.snapshot(tabID, true, "", "", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("baseline snapshot: %v", err)
	}
	if len(baseSnap.Nodes) == 0 {
		t.Fatal("expected baseline nodes")
	}

	sess, err := d.tabSession(tabID)
	if err != nil {
		t.Fatalf("tabSession: %v", err)
	}
	ctx := context.Background()

	// 2. Minor mutation under threshold: change only 1 item out of 10 (~10% changed < 70%)
	err = sess.eval(ctx, `document.querySelectorAll('li')[0].textContent = 'Modified Item 1'; true;`, nil)
	if err != nil {
		t.Fatalf("eval minor mutation: %v", err)
	}

	minorSnap, err := d.snapshot(tabID, false, "", "", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("minor mutation snapshot: %v", err)
	}
	if minorSnap.PageReplaced {
		t.Errorf("expected diff for minor mutation, got PageReplaced=true")
	}
	if minorSnap.Diff == "" || (!strings.Contains(minorSnap.Diff, "~") && !strings.Contains(minorSnap.Diff, "+") && !strings.Contains(minorSnap.Diff, "-")) {
		t.Errorf("expected +/-/~ delta diff in minorSnap.Diff, got: %q", minorSnap.Diff)
	}

	// 3. Wholesale mutation over threshold: replace entire body (deletes old list, > 70% changed)
	err = sess.eval(ctx, `document.body.innerHTML = '<h1>Entirely Different Page</h1><p>All old content is gone</p>'; true;`, nil)
	if err != nil {
		t.Fatalf("eval wholesale mutation: %v", err)
	}

	majorSnap, err := d.snapshot(tabID, false, "", "", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("wholesale mutation snapshot: %v", err)
	}
	if !majorSnap.PageReplaced {
		t.Errorf("expected PageReplaced=true for wholesale mutation, got false (Diff: %q)", majorSnap.Diff)
	}
	if majorSnap.Diff != "" {
		t.Errorf("expected majorSnap.Diff to be empty on wholesale replace, got: %q", majorSnap.Diff)
	}
	if !strings.HasPrefix(majorSnap.Text(), "[page replaced — full snapshot]\n") {
		t.Errorf("expected [page replaced — full snapshot] prefix, got: %s", majorSnap.Text())
	}
}
