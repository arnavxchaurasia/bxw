package contextmgr

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestContextSaveAndLoad(t *testing.T) {
	sessionID := "test-session-123"
	ctx := SessionContext{
		SessionID: sessionID,
		Browser:   "edge",
		ActiveTab: "tab-42",
		OpenTabs: []TabSummary{
			{ID: "tab-42", Title: "Example", URL: "https://example.com"},
		},
		LastActions: []ActionSummary{
			{Action: "click e1", Result: "success", Time: time.Now()},
		},
	}

	if err := SaveSession(ctx); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	loaded, err := LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	if loaded.Browser != "edge" || loaded.ActiveTab != "tab-42" {
		t.Errorf("loaded context mismatch: %+v", loaded)
	}

	sum := loaded.Summary()
	if !strings.Contains(sum, "tab-42") || !strings.Contains(sum, "Example") {
		t.Errorf("summary missing expected fields: %s", sum)
	}
}

func TestF2DurableSessionStateModel(t *testing.T) {
	sessionID := fmt.Sprintf("test-f2-%d", time.Now().UnixNano())

	// 1. First action
	err := RecordAction("edge", sessionID, "tab-1", "click e5", "clicked e5 -> 2 nodes changed", "fill form", "")
	if err != nil {
		t.Fatalf("record action 1 failed: %v", err)
	}

	// 2. Second action
	err = RecordAction("edge", sessionID, "tab-1", "fill e2 Alice", "filled e2 Alice -> done", "submit form", "")
	if err != nil {
		t.Fatalf("record action 2 failed: %v", err)
	}

	// 3. Verify state round-trip from disk
	sc, err := LoadSession(sessionID)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if len(sc.LastActions) != 2 {
		t.Fatalf("expected 2 actions recorded, got %d", len(sc.LastActions))
	}
	if sc.LastActions[0].Action != "click e5" || sc.LastActions[1].Action != "fill e2 Alice" {
		t.Errorf("unexpected actions sequence: %+v", sc.LastActions)
	}

	// 4. Verify append-only markdown journal
	notes, err := ReadNotes(sessionID)
	if err != nil {
		t.Fatalf("read notes failed: %v", err)
	}
	if !strings.Contains(notes, "clicked e5 -> 2 nodes changed") || !strings.Contains(notes, "filled e2 Alice -> done") {
		t.Fatalf("markdown journal missing actions: %s", notes)
	}

	// Count note headers
	hCount := strings.Count(notes, "### [")
	if hCount != 2 {
		t.Fatalf("expected 2 note entries, got %d:\n%s", hCount, notes)
	}

	// 5. Add a 3rd "correction" note — must append, never overwrite earlier entries
	err = AppendNote(sessionID, NoteEntry{
		Timestamp: time.Now().UTC(),
		Author:    "agent",
		Action:    "correction",
		Summary:   "Correction: previous input was intended for e3 not e2",
		Next:      "fill e3",
	})
	if err != nil {
		t.Fatalf("append correction note failed: %v", err)
	}

	notesAfter, err := ReadNotes(sessionID)
	if err != nil {
		t.Fatalf("read notes after correction failed: %v", err)
	}
	if strings.Count(notesAfter, "### [") != 3 {
		t.Fatalf("expected 3 note entries after correction, got %d:\n%s", strings.Count(notesAfter, "### ["), notesAfter)
	}
	if !strings.Contains(notesAfter, "clicked e5") || !strings.Contains(notesAfter, "Correction: previous input") {
		t.Fatalf("earlier notes lost after correction:\n%s", notesAfter)
	}
}
