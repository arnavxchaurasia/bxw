package contextmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bxw/internal/osport"
)

// TabSummary is a lightweight overview of a browser tab.
type TabSummary struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Profile string `json:"profile,omitempty"`
}

// ActionSummary records a terse action taken by an agent.
type ActionSummary struct {
	Action string    `json:"action"` // e.g. "click e12", "fill e4"
	Result string    `json:"result"` // e.g. "navigated to https://..."
	Time   time.Time `json:"time"`
}

// SessionModel represents the structured machine state (Flow-style two-store split: store 1).
type SessionModel struct {
	SessionID    string            `json:"session_id"`
	Browser      string            `json:"browser"`
	ActiveTab    string            `json:"active_tab"`
	URL          string            `json:"url,omitempty"`
	Title        string            `json:"title,omitempty"`
	LastSnapshot string            `json:"last_snapshot_digest,omitempty"` // blob hash or AXTree digest
	RegionHashes map[string]string `json:"region_hashes,omitempty"`        // stable-region memoization hashes (C4)
	OpenTabs     []TabSummary      `json:"open_tabs,omitempty"`
	LastActions  []ActionSummary   `json:"last_actions,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// SessionContext aliases SessionModel for backwards compatibility.
type SessionContext = SessionModel

// NoteEntry represents a single append-only dated note (Flow-style two-store split: store 2).
type NoteEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Author    string    `json:"author,omitempty"` // "agent" or "user"
	Action    string    `json:"action,omitempty"` // e.g. "click e5", "goto /dashboard"
	Summary   string    `json:"summary"`          // What changed / what was observed
	Next      string    `json:"next,omitempty"`   // What's next
	BlockedOn string    `json:"blocked_on,omitempty"`
	BlobURI   string    `json:"blob_uri,omitempty"` // e.g. "bxw://cache/<hash>"
}

// SaveSession persists structured machine state to context/<session>.json atomically.
func SaveSession(sc SessionModel) error {
	p := osport.Current()
	ctxDir := p.ContextDir()
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		return err
	}

	sc.UpdatedAt = time.Now().UTC()
	if sc.SessionID == "" {
		sc.SessionID = "default"
	}

	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}

	target := filepath.Join(ctxDir, sc.SessionID+".json")
	tmp := fmt.Sprintf("%s.tmp.%d", target, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// LoadSession loads a session context by ID.
func LoadSession(sessionID string) (SessionModel, error) {
	if sessionID == "" {
		sessionID = "default"
	}
	p := osport.Current()
	target := filepath.Join(p.ContextDir(), sessionID+".json")
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionModel{
				SessionID:    sessionID,
				RegionHashes: make(map[string]string),
				UpdatedAt:    time.Now().UTC(),
			}, nil
		}
		return SessionModel{}, err
	}

	var sc SessionModel
	if err := json.Unmarshal(data, &sc); err != nil {
		return SessionModel{}, err
	}
	if sc.RegionHashes == nil {
		sc.RegionHashes = make(map[string]string)
	}
	return sc, nil
}

// AppendNote appends a human-readable dated note to context/<session>.md.
// Notes are strictly append-only: corrections are added as new notes, never in-place edits.
func AppendNote(sessionID string, note NoteEntry) error {
	if sessionID == "" {
		sessionID = "default"
	}
	if note.Timestamp.IsZero() {
		note.Timestamp = time.Now().UTC()
	}
	if note.Author == "" {
		note.Author = "agent"
	}

	p := osport.Current()
	ctxDir := p.ContextDir()
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		return err
	}

	mdTarget := filepath.Join(ctxDir, sessionID+".md")
	f, err := os.OpenFile(mdTarget, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var sb strings.Builder
	ts := note.Timestamp.Format("2006-01-02 15:04:05 UTC")
	sb.WriteString(fmt.Sprintf("\n### [%s] %s\n", ts, note.Author))
	if note.Action != "" {
		sb.WriteString(fmt.Sprintf("- **Action:** `%s`\n", note.Action))
	}
	if note.Summary != "" {
		sb.WriteString(fmt.Sprintf("- **Summary:** %s\n", note.Summary))
	}
	if note.Next != "" {
		sb.WriteString(fmt.Sprintf("- **Next:** %s\n", note.Next))
	}
	if note.BlockedOn != "" {
		sb.WriteString(fmt.Sprintf("- **Blocked-on:** %s\n", note.BlockedOn))
	}
	if note.BlobURI != "" {
		sb.WriteString(fmt.Sprintf("- **Resource:** `%s`\n", note.BlobURI))
	}

	_, err = f.WriteString(sb.String())
	return err
}

// ReadNotes loads the entire narrative markdown journal for a session.
func ReadNotes(sessionID string) (string, error) {
	if sessionID == "" {
		sessionID = "default"
	}
	p := osport.Current()
	mdTarget := filepath.Join(p.ContextDir(), sessionID+".md")
	data, err := os.ReadFile(mdTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// RecordAction updates structured machine state and appends a narrative note in tandem.
func RecordAction(browser, sessionID, tabID, action, summary, next, blobURI string) error {
	if sessionID == "" {
		sessionID = "default"
	}

	now := time.Now().UTC()
	// 1. Append dated note
	note := NoteEntry{
		Timestamp: now,
		Author:    "agent",
		Action:    action,
		Summary:   summary,
		Next:      next,
		BlobURI:   blobURI,
	}
	if err := AppendNote(sessionID, note); err != nil {
		return err
	}

	// 2. Update machine state
	sc, err := LoadSession(sessionID)
	if err != nil {
		sc = SessionModel{SessionID: sessionID}
	}
	if browser != "" {
		sc.Browser = browser
	}
	if tabID != "" {
		sc.ActiveTab = tabID
	}
	sc.LastActions = append(sc.LastActions, ActionSummary{
		Action: action,
		Result: summary,
		Time:   now,
	})
	if len(sc.LastActions) > 20 {
		sc.LastActions = sc.LastActions[len(sc.LastActions)-20:]
	}
	return SaveSession(sc)
}

// ListSessions returns all stored sessions sorted by most recent first.
func ListSessions() ([]SessionModel, error) {
	p := osport.Current()
	ctxDir := p.ContextDir()
	entries, err := os.ReadDir(ctxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionModel
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			name := strings.TrimSuffix(e.Name(), ".json")
			sc, err := LoadSession(name)
			if err == nil {
				sessions = append(sessions, sc)
			}
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// Summary formats a compact 5-10 line summary of the session context for the model.
func (sc SessionModel) Summary() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session: %s (Browser: %s, ActiveTab: %s)\n", sc.SessionID, sc.Browser, sc.ActiveTab))
	if sc.URL != "" {
		sb.WriteString(fmt.Sprintf("Current Page: %s (%s)\n", sc.Title, sc.URL))
	}
	if len(sc.OpenTabs) > 0 {
		sb.WriteString(fmt.Sprintf("Open Tabs (%d):\n", len(sc.OpenTabs)))
		for _, t := range sc.OpenTabs {
			sb.WriteString(fmt.Sprintf("  [%s] %s (%s)\n", t.ID, t.Title, t.URL))
		}
	}
	if len(sc.LastActions) > 0 {
		sb.WriteString("Recent Actions:\n")
		limit := len(sc.LastActions)
		if limit > 5 {
			limit = 5
		}
		for i := len(sc.LastActions) - limit; i < len(sc.LastActions); i++ {
			a := sc.LastActions[i]
			sb.WriteString(fmt.Sprintf("  • %s -> %s\n", a.Action, a.Result))
		}
	}
	return strings.TrimSpace(sb.String())
}
