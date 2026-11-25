package daemon

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bxw/internal/contextmgr"
	"bxw/internal/history"
	"bxw/internal/osport"
)

// RecallParams specifies search and filtering criteria for episodic recall (C5).
type RecallParams struct {
	Session string `json:"session,omitempty"`
	Tab     string `json:"tab,omitempty"`
	Grep    string `json:"grep,omitempty"`
	Since   string `json:"since,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Browser string `json:"browser,omitempty"`
}

// RecallItem represents an individual event in the consolidated episodic timeline.
// Uses lazy pointers rather than dumping massive raw payloads.
type RecallItem struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`            // "note" or "history"
	Action    string    `json:"action,omitempty"`  // e.g. "click e5", "navigate"
	Summary   string    `json:"summary"`           // Terse 1-line summary
	Pointer   string    `json:"pointer,omitempty"` // file path or URI pointer
	Score     int       `json:"score"`             // Relevance rank score (C1-style)
}

// RecallResult is the bounded, ranked episodic recall output.
type RecallResult struct {
	SessionID string       `json:"session_id,omitempty"`
	Browser   string       `json:"browser,omitempty"`
	Takeaway  string       `json:"takeaway,omitempty"` // distilled closeout takeaway if available
	Total     int          `json:"total"`
	Items     []RecallItem `json:"items"`
}

// Text formats the recall result into a bounded, compact human-readable timeline.
func (r RecallResult) Text() string {
	var sb strings.Builder
	if r.SessionID != "" {
		sb.WriteString(fmt.Sprintf("Episodic Recall (Session: %s", r.SessionID))
		if r.Browser != "" {
			sb.WriteString(fmt.Sprintf(", Browser: %s", r.Browser))
		}
		sb.WriteString(")\n")
	} else {
		sb.WriteString("Episodic Recall\n")
	}

	if r.Takeaway != "" {
		sb.WriteString(fmt.Sprintf("Distilled Takeaway: %s\n", r.Takeaway))
	}

	if len(r.Items) == 0 {
		sb.WriteString("(no matching recall events found)\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Timeline (%d items shown, %d total):\n", len(r.Items), r.Total))
	for _, it := range r.Items {
		ts := it.Timestamp.Format("15:04:05")
		src := it.Source
		action := it.Action
		if action == "" {
			action = "-"
		}
		ptr := ""
		if it.Pointer != "" {
			ptr = fmt.Sprintf(" [%s]", it.Pointer)
		}
		sb.WriteString(fmt.Sprintf("  [%s] (%s) %s -> %s%s\n", ts, src, action, it.Summary, ptr))
	}
	return sb.String()
}

// Recall consolidates dated notes and navigation history into a bounded, ranked timeline.
func (d *Daemon) Recall(params RecallParams) (RecallResult, error) {
	sessionID := params.Session
	if sessionID == "" {
		sessionID = "default"
	}
	browserName := params.Browser
	if browserName == "" && d != nil && d.br.Name != "" {
		browserName = d.br.Name
	}
	if browserName == "" {
		browserName = "edge"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20 // default bounded limit
	}

	var sinceTime time.Time
	if params.Since != "" {
		if t, err := time.Parse(time.RFC3339, params.Since); err == nil {
			sinceTime = t
		} else if dur, err := time.ParseDuration(params.Since); err == nil {
			sinceTime = time.Now().Add(-dur)
		}
	}

	grepLower := strings.ToLower(strings.TrimSpace(params.Grep))
	p := osport.Current()
	mdPath := filepath.Join(p.ContextDir(), sessionID+".md")

	var allItems []RecallItem

	// 1. Load notes from F2 dated notes
	notesMD, _ := contextmgr.ReadNotes(sessionID)
	notes := contextmgr.ParseNotes(notesMD)
	for _, n := range notes {
		if !sinceTime.IsZero() && n.Timestamp.Before(sinceTime) {
			continue
		}
		if grepLower != "" {
			match := strings.Contains(strings.ToLower(n.Action), grepLower) ||
				strings.Contains(strings.ToLower(n.Summary), grepLower) ||
				strings.Contains(strings.ToLower(n.Next), grepLower) ||
				strings.Contains(strings.ToLower(n.BlockedOn), grepLower)
			if !match {
				continue
			}
		}

		// Calculate relevance score
		score := 10
		if grepLower != "" {
			if strings.EqualFold(n.Action, grepLower) || strings.EqualFold(n.Summary, grepLower) {
				score += 50
			} else if strings.Contains(strings.ToLower(n.Action), grepLower) {
				score += 30
			} else if strings.Contains(strings.ToLower(n.Summary), grepLower) {
				score += 20
			}
		}

		ptr := n.BlobURI
		if ptr == "" {
			ptr = mdPath
		}

		summary := n.Summary
		if summary == "" {
			summary = n.Action
		}

		allItems = append(allItems, RecallItem{
			Timestamp: n.Timestamp,
			Source:    "note",
			Action:    n.Action,
			Summary:   summary,
			Pointer:   ptr,
			Score:     score,
		})
	}

	// 2. Load navigation history
	histFilter := history.Filter{
		Since: sinceTime,
		Grep:  params.Grep,
		Limit: 100, // retrieve candidate pool
	}
	histEntries, _ := history.Query(browserName, histFilter)
	for _, h := range histEntries {
		if params.Tab != "" && !strings.EqualFold(h.TabID, params.Tab) && !strings.HasPrefix(strings.ToLower(h.TabID), strings.ToLower(params.Tab)) {
			continue
		}

		score := 5
		if grepLower != "" {
			if strings.Contains(strings.ToLower(h.URL), grepLower) {
				score += 25
			}
			if strings.Contains(strings.ToLower(h.Title), grepLower) {
				score += 15
			}
		}

		summary := h.Title
		if summary == "" {
			summary = h.URL
		} else {
			summary = fmt.Sprintf("%s (%s)", h.Title, h.URL)
		}

		allItems = append(allItems, RecallItem{
			Timestamp: h.Timestamp,
			Source:    "history",
			Action:    "navigate",
			Summary:   summary,
			Pointer:   h.URL,
			Score:     score,
		})
	}

	total := len(allItems)

	// 3. Deterministic ranking:
	// If grep was provided, sort by relevance score DESC, then Timestamp DESC.
	// Otherwise, sort chronologically by Timestamp ASC (or most recent depending on view).
	if grepLower != "" {
		sort.SliceStable(allItems, func(i, j int) bool {
			if allItems[i].Score != allItems[j].Score {
				return allItems[i].Score > allItems[j].Score
			}
			return allItems[i].Timestamp.After(allItems[j].Timestamp)
		})
	} else {
		// Default chronological ordering
		sort.SliceStable(allItems, func(i, j int) bool {
			return allItems[i].Timestamp.Before(allItems[j].Timestamp)
		})
	}

	if limit > 0 && len(allItems) > limit {
		if grepLower != "" {
			allItems = allItems[:limit]
		} else {
			// Keep the most recent `limit` items for chronological view
			allItems = allItems[len(allItems)-limit:]
		}
	}

	// Load takeaway if session exists
	takeaway := ""
	if sc, err := contextmgr.LoadSession(sessionID); err == nil {
		takeaway = sc.Takeaway
	}

	return RecallResult{
		SessionID: sessionID,
		Browser:   browserName,
		Takeaway:  takeaway,
		Total:     total,
		Items:     allItems,
	}, nil
}
