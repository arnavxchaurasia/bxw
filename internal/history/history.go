package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bxw/internal/osport"
)

// Entry records a single navigation event.
type Entry struct {
	Timestamp time.Time `json:"ts"`
	Browser   string    `json:"browser"`
	Profile   string    `json:"profile"`
	TabID     string    `json:"tabId"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Source    string    `json:"source"` // "agent" or "user"
}

// Filter specifies criteria for querying history.
type Filter struct {
	Since   time.Time
	Grep    string
	Profile string
	Limit   int
}

var mu sync.Mutex

// Log records a navigation event to the browser's history log.
func Log(browser, profile, tabID, targetURL, title, source string) error {
	mu.Lock()
	defer mu.Unlock()

	p := osport.Current()
	histDir := p.HistoryDir()
	if err := os.MkdirAll(histDir, 0o755); err != nil {
		return err
	}

	entry := Entry{
		Timestamp: time.Now().UTC(),
		Browser:   browser,
		Profile:   profile,
		TabID:     tabID,
		URL:       targetURL,
		Title:     title,
		Source:    source,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Join(histDir, browser+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(line, '\n'))
	return err
}

// Query reads history events matching the filter.
func Query(browser string, filter Filter) ([]Entry, error) {
	mu.Lock()
	defer mu.Unlock()

	p := osport.Current()
	target := filepath.Join(p.HistoryDir(), browser+".jsonl")
	f, err := os.Open(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	grepLower := strings.ToLower(filter.Grep)
	var matches []Entry

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}

		if !filter.Since.IsZero() && e.Timestamp.Before(filter.Since) {
			continue
		}
		if filter.Profile != "" && !strings.EqualFold(e.Profile, filter.Profile) {
			continue
		}
		if grepLower != "" {
			if !strings.Contains(strings.ToLower(e.URL), grepLower) && !strings.Contains(strings.ToLower(e.Title), grepLower) {
				continue
			}
		}

		matches = append(matches, e)
	}

	if filter.Limit > 0 && len(matches) > filter.Limit {
		matches = matches[len(matches)-filter.Limit:]
	}

	return matches, nil
}

// Clean removes entries older than retention.
func Clean(browser string, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	entries, err := Query(browser, Filter{})
	if err != nil || len(entries) == 0 {
		return 0, err
	}

	mu.Lock()
	defer mu.Unlock()

	var kept []Entry
	removed := 0
	for _, e := range entries {
		if e.Timestamp.After(cutoff) {
			kept = append(kept, e)
		} else {
			removed++
		}
	}

	if removed == 0 {
		return 0, nil
	}

	p := osport.Current()
	target := filepath.Join(p.HistoryDir(), browser+".jsonl")
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	for _, e := range kept {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
	}

	return removed, nil
}
