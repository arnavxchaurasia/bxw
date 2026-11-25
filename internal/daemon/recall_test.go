package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"bxw/internal/browser"
	"bxw/internal/contextmgr"
	"bxw/internal/history"
)

// TestC5RecallFilteringAndBudget verifies that recall consolidates notes and history
// into a bounded, relevance-ranked timeline respecting --grep, --since, and --limit.
func TestC5RecallFilteringAndBudget(t *testing.T) {
	sessionID := fmt.Sprintf("test-c5-session-%d", time.Now().UnixNano())
	browserName := "test-c5-browser"

	// 1. Seed dated notes via contextmgr
	now := time.Now().UTC()
	err := contextmgr.AppendNote(sessionID, contextmgr.NoteEntry{
		Timestamp: now.Add(-30 * time.Minute),
		Author:    "agent",
		Action:    "click e1",
		Summary:   "Clicked search submit button",
		Next:      "inspect results table",
	})
	if err != nil {
		t.Fatalf("seed note 1 failed: %v", err)
	}

	err = contextmgr.AppendNote(sessionID, contextmgr.NoteEntry{
		Timestamp: now.Add(-20 * time.Minute),
		Author:    "agent",
		Action:    "fill e2 query",
		Summary:   "Filled query input with pricing terms",
		Next:      "submit query",
	})
	if err != nil {
		t.Fatalf("seed note 2 failed: %v", err)
	}

	err = contextmgr.AppendNote(sessionID, contextmgr.NoteEntry{
		Timestamp: now.Add(-10 * time.Minute),
		Author:    "agent",
		Action:    "click e5",
		Summary:   "Downloaded quarterly pricing report CSV",
		BlobURI:   "bxw://cache/report123.csv",
	})
	if err != nil {
		t.Fatalf("seed note 3 failed: %v", err)
	}

	// 2. Seed history
	_ = history.Log(browserName, "Default", "tab-1", "https://example.com/search", "Search Page", "agent")
	_ = history.Log(browserName, "Default", "tab-1", "https://example.com/pricing", "Pricing Overview", "agent")

	d := &Daemon{
		br: browser.Browser{Name: browserName},
	}

	// 3. Recall with no filter: should return all events up to limit
	res, err := d.Recall(RecallParams{
		Session: sessionID,
		Browser: browserName,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(res.Items) < 3 {
		t.Fatalf("expected at least 3 items in recall, got %d", len(res.Items))
	}

	// 4. Test bounded limit
	resBounded, err := d.Recall(RecallParams{
		Session: sessionID,
		Browser: browserName,
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("recall bounded failed: %v", err)
	}
	if len(resBounded.Items) > 2 {
		t.Fatalf("expected <= 2 items, got %d", len(resBounded.Items))
	}

	// 5. Test --grep ranking
	resGrep, err := d.Recall(RecallParams{
		Session: sessionID,
		Browser: browserName,
		Grep:    "pricing",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("recall with grep failed: %v", err)
	}
	if len(resGrep.Items) == 0 {
		t.Fatalf("expected grep matches for 'pricing', got 0")
	}
	for _, it := range resGrep.Items {
		lowerSum := strings.ToLower(it.Summary)
		lowerAct := strings.ToLower(it.Action)
		if !strings.Contains(lowerSum, "pricing") && !strings.Contains(lowerAct, "pricing") && !strings.Contains(strings.ToLower(it.Pointer), "pricing") {
			t.Errorf("item does not match grep 'pricing': %+v", it)
		}
	}

	// 6. Test lazy pointers in output text
	txt := res.Text()
	if !strings.Contains(txt, "Timeline") || !strings.Contains(txt, "[") {
		t.Fatalf("expected timeline formatted text, got:\n%s", txt)
	}
	if !strings.Contains(txt, "bxw://cache/report123.csv") && !strings.Contains(txt, ".md") {
		t.Fatalf("expected lazy pointer in output text, got:\n%s", txt)
	}
}

// TestC5DoneCloseout asserts that `done` writes a compact takeaway to SessionModel,
// appends a closeout note to markdown narrative, leaves raw notes intact, and updates recall.
func TestC5DoneCloseout(t *testing.T) {
	sessionID := fmt.Sprintf("test-c5-done-%d", time.Now().UnixNano())

	// 1. Seed dated notes
	_ = contextmgr.AppendNote(sessionID, contextmgr.NoteEntry{
		Timestamp: time.Now().UTC().Add(-5 * time.Minute),
		Author:    "agent",
		Action:    "click e1",
		Summary:   "Selected enterprise subscription tier",
	})
	_ = contextmgr.AppendNote(sessionID, contextmgr.NoteEntry{
		Timestamp: time.Now().UTC().Add(-2 * time.Minute),
		Author:    "agent",
		Action:    "fill e4 corporate-card",
		Summary:   "Payment details completed",
	})

	rawBefore, err := contextmgr.ReadNotes(sessionID)
	if err != nil {
		t.Fatalf("read raw notes failed: %v", err)
	}
	hCountBefore := strings.Count(rawBefore, "### [")
	if hCountBefore != 2 {
		t.Fatalf("expected 2 raw notes before done, got %d", hCountBefore)
	}

	// 2. Perform closeout
	takeaway := "Successfully configured enterprise plan and saved billing info."
	err = contextmgr.CloseoutSession(sessionID, takeaway)
	if err != nil {
		t.Fatalf("CloseoutSession failed: %v", err)
	}

	// 3. Verify SessionModel contains durable takeaway
	sc, err := contextmgr.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}
	if sc.Takeaway != takeaway {
		t.Fatalf("expected takeaway %q, got %q", takeaway, sc.Takeaway)
	}

	// 4. Verify raw notes are intact and append-only note added
	rawAfter, err := contextmgr.ReadNotes(sessionID)
	if err != nil {
		t.Fatalf("read raw notes after done failed: %v", err)
	}
	hCountAfter := strings.Count(rawAfter, "### [")
	if hCountAfter != 3 {
		t.Fatalf("expected 3 raw notes after done (append-only), got %d:\n%s", hCountAfter, rawAfter)
	}
	if !strings.Contains(rawAfter, "Selected enterprise subscription tier") || !strings.Contains(rawAfter, takeaway) {
		t.Fatalf("raw notes lost or takeaway missing in markdown:\n%s", rawAfter)
	}

	// 5. Verify recall surfaces takeaway
	d := &Daemon{}
	rec, err := d.Recall(RecallParams{
		Session: sessionID,
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if rec.Takeaway != takeaway {
		t.Fatalf("recall output missing takeaway: expected %q, got %q", takeaway, rec.Takeaway)
	}
	recTxt := rec.Text()
	if !strings.Contains(recTxt, takeaway) {
		t.Fatalf("recall text does not display takeaway:\n%s", recTxt)
	}
}
