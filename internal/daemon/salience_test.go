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
)

func TestC3SalienceTiers(t *testing.T) {
	if NodeSalienceTier("button") != 1 {
		t.Errorf("expected button to be Tier 1")
	}
	if NodeSalienceTier("link") != 1 {
		t.Errorf("expected link to be Tier 1")
	}
	if NodeSalienceTier("heading") != 2 {
		t.Errorf("expected heading to be Tier 2")
	}
	if NodeSalienceTier("navigation") != 2 {
		t.Errorf("expected navigation to be Tier 2")
	}
	if NodeSalienceTier("paragraph") != 3 {
		t.Errorf("expected paragraph to be Tier 3")
	}
	if NodeSalienceTier("StaticText") != 3 {
		t.Errorf("expected StaticText to be Tier 3")
	}
}

func TestC3BudgetSnapshotGolden(t *testing.T) {
	// Build a synthetic tree:
	// e1 main (depth 0, structure)
	//   e2 heading (depth 1, structure)
	//   e3 button "Submit" (depth 1, interactive)
	//   e4 region "Sidebar" (depth 1, structure)
	//     e5 text "Some long article paragraph 1" (depth 2, text)
	//     e6 text "Some long article paragraph 2" (depth 2, text)
	//     e7 text "Some long article paragraph 3" (depth 2, text)
	//     e8 text "Some long article paragraph 4" (depth 2, text)
	//     e9 text "Some long article paragraph 5" (depth 2, text)
	//   e10 link "Help" (depth 1, interactive)
	nodes := []SnapNode{
		{Ref: "e1", Depth: 0, Role: "main", Name: "Main Area"},
		{Ref: "e2", Depth: 1, Role: "heading", Name: "Title"},
		{Ref: "e3", Depth: 1, Role: "button", Name: "Submit"},
		{Ref: "e4", Depth: 1, Role: "region", Name: "Sidebar"},
		{Ref: "e5", Depth: 2, Role: "text", Name: "Some long article paragraph 1"},
		{Ref: "e6", Depth: 2, Role: "text", Name: "Some long article paragraph 2"},
		{Ref: "e7", Depth: 2, Role: "text", Name: "Some long article paragraph 3"},
		{Ref: "e8", Depth: 2, Role: "text", Name: "Some long article paragraph 4"},
		{Ref: "e9", Depth: 2, Role: "text", Name: "Some long article paragraph 5"},
		{Ref: "e10", Depth: 1, Role: "link", Name: "Help"},
	}

	// 1. Unlimited / huge budget returns all nodes, no elided
	keptFull, elidedFull := BudgetSnapshot(nodes, 10000)
	if len(keptFull) != len(nodes) || len(elidedFull) != 0 {
		t.Errorf("expected full nodes, got len %d, elided %v", len(keptFull), elidedFull)
	}

	// 2. Budget constraining text nodes:
	// Interactive (e3, e10) + Structure (e1, e2, e4) should survive before text (e5-e9)
	kept, elided := BudgetSnapshot(nodes, 40)

	totalEst := 0
	for _, n := range kept {
		totalEst += NodeTokenEstimate(n)
	}
	if totalEst > 40 {
		t.Errorf("estimated tokens %d exceeds budget 40", totalEst)
	}

	// Check interactive nodes survived
	hasSubmit := false
	hasHelp := false
	for _, n := range kept {
		if n.Ref == "e3" {
			hasSubmit = true
		}
		if n.Ref == "e10" {
			hasHelp = true
		}
	}
	if !hasSubmit || !hasHelp {
		t.Errorf("expected interactive nodes e3 and e10 to survive, kept: %+v", kept)
	}

	// Check elided counts under e4
	if elided["e4"] == 0 {
		t.Errorf("expected elided descendants under e4, got %v", elided)
	}

	// 3. Document order preserved
	for i := 1; i < len(kept); i++ {
		prevNum := 0
		currNum := 0
		fmt.Sscanf(kept[i-1].Ref, "e%d", &prevNum)
		fmt.Sscanf(kept[i].Ref, "e%d", &currNum)
		if currNum <= prevNum {
			t.Errorf("order not preserved: %s after %s", kept[i].Ref, kept[i-1].Ref)
		}
	}

	// 4. Determinism
	kept2, elided2 := BudgetSnapshot(nodes, 40)
	if len(kept) != len(kept2) {
		t.Fatalf("determinism failure: len %d vs %d", len(kept), len(kept2))
	}
	for i := range kept {
		if kept[i].Ref != kept2[i].Ref {
			t.Errorf("determinism mismatch at %d: %s vs %s", i, kept[i].Ref, kept2[i].Ref)
		}
	}
	for k, v := range elided {
		if elided2[k] != v {
			t.Errorf("determinism mismatch in elided[%s]: %d vs %d", k, v, elided2[k])
		}
	}

	// 5. Rendering format contains drill-down hint
	snap := Snapshot{
		Nodes:  kept,
		Elided: elided,
	}
	rendered := snap.Text()
	wantMarker := fmt.Sprintf("[+ %d more — snapshot --ref e4]", elided["e4"])
	if !strings.Contains(rendered, wantMarker) {
		t.Errorf("rendered snapshot missing drill-down marker %q; got:\n%s", wantMarker, rendered)
	}

	// 6. Drill-down: FilterSubtree on e4 returns the elided subtree
	subtree := FilterSubtree(nodes, "e4")
	if len(subtree) != 6 { // e4 itself + 5 text children
		t.Errorf("expected 6 nodes in e4 subtree, got %d", len(subtree))
	}
}

func TestC3SnapshotIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live browser action test in short mode")
	}

	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no supported Chromium browser found on this machine")
	}

	targetBrowser := bs[0]

	// Server serving a page with headings, interactive buttons, and long text lists
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>C3 Budget Test</title></head>
<body>
  <main>
    <h1>Page Title</h1>
    <button id="primary-action">Main Action</button>
    <section aria-label="Article Section">
      <p>Paragraph 1 of long text content that takes tokens to represent.</p>
      <p>Paragraph 2 of long text content that takes tokens to represent.</p>
      <p>Paragraph 3 of long text content that takes tokens to represent.</p>
      <p>Paragraph 4 of long text content that takes tokens to represent.</p>
      <p>Paragraph 5 of long text content that takes tokens to represent.</p>
    </section>
    <a href="#help">Help Link</a>
  </main>
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

	// 1. Take budgeted snapshot with a small budget
	snap, err := d.snapshot(tabID, true, "", "", 35, 10*time.Second)
	if err != nil {
		t.Fatalf("budgeted snapshot failed: %v", err)
	}

	rendered := snap.Text()
	if len(snap.Elided) == 0 {
		t.Errorf("expected elided nodes in budgeted snapshot, got none:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[+ ") || !strings.Contains(rendered, "more — snapshot --ref") {
		t.Errorf("expected drill-down marker in rendered snapshot:\n%s", rendered)
	}

	// Find the elided ref
	var elidedRef string
	for ref := range snap.Elided {
		elidedRef = ref
		break
	}

	// 2. Drill down using --ref <elidedRef>
	drilled, err := d.snapshot(tabID, true, elidedRef, "", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("drilled snapshot failed: %v", err)
	}
	if len(drilled.Nodes) == 0 {
		t.Fatalf("expected nodes in drilled snapshot for ref %s", elidedRef)
	}
	if drilled.Nodes[0].Ref != elidedRef {
		t.Errorf("expected drilled snapshot root to be %s, got %s", elidedRef, drilled.Nodes[0].Ref)
	}
}
