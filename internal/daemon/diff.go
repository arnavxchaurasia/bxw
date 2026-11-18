package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/osport"
)

// TabState represents the persisted snapshot baseline and ref map stored on disk.
type TabState struct {
	URL       string                       `json:"url"`
	Title     string                       `json:"title"`
	Hash      string                       `json:"hash"`
	Timestamp time.Time                    `json:"timestamp"`
	Nodes     []SnapNode                   `json:"nodes"`
	RefMap    map[string]cdp.BackendNodeID `json:"ref_map"`
}

// ComputeHash computes a deterministic SHA-256 hash of a node slice.
func ComputeHash(nodes []SnapNode) string {
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("%d:%s:%s:%s:%s\n", n.Depth, n.Role, n.Name, n.Value, strings.Join(n.Props, ",")))
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:16]) // 32 hex chars
}

// SaveTabState writes the baseline snapshot and refs for a tab to disk.
func SaveTabState(browser, tabID string, snap Snapshot, refs map[string]cdp.BackendNodeID) error {
	p := osport.Current()
	stateDir := p.StateDir(browser)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	st := TabState{
		URL:       snap.URL,
		Title:     snap.Title,
		Hash:      ComputeHash(snap.Nodes),
		Timestamp: time.Now().UTC(),
		Nodes:     snap.Nodes,
		RefMap:    refs,
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}

	target := filepath.Join(stateDir, shortTabID(tabID)+".json")
	return os.WriteFile(target, data, 0o644)
}

// LoadTabState reads the stored baseline snapshot for a tab from disk.
func LoadTabState(browser, tabID string) (TabState, error) {
	p := osport.Current()
	target := filepath.Join(p.StateDir(browser), shortTabID(tabID)+".json")
	data, err := os.ReadFile(target)
	if err != nil {
		return TabState{}, err
	}

	var st TabState
	if err := json.Unmarshal(data, &st); err != nil {
		return TabState{}, err
	}
	return st, nil
}

// DeleteTabState removes the stored baseline snapshot for a tab from disk.
func DeleteTabState(browser, tabID string) error {
	p := osport.Current()
	target := filepath.Join(p.StateDir(browser), shortTabID(tabID)+".json")
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DiffSnapshots produces a terse delta between a baseline and the current snapshot.
func DiffSnapshots(baseline []SnapNode, current []SnapNode) (diff string, changedCount int) {
	if len(baseline) == 0 {
		return "", len(current)
	}

	baseMap := make(map[string]SnapNode, len(baseline))
	for _, n := range baseline {
		baseMap[n.Ref] = n
	}

	currMap := make(map[string]SnapNode, len(current))
	for _, n := range current {
		currMap[n.Ref] = n
	}

	var sb strings.Builder

	// Check additions & modifications
	for _, c := range current {
		b, exists := baseMap[c.Ref]
		if !exists {
			sb.WriteString("+ ")
			sb.WriteString(formatNode(c))
			sb.WriteByte('\n')
			changedCount++
		} else if b.Name != c.Name || b.Value != c.Value || b.Role != c.Role || strings.Join(b.Props, ",") != strings.Join(c.Props, ",") {
			sb.WriteString("~ ")
			sb.WriteString(formatNode(c))
			sb.WriteByte('\n')
			changedCount++
		}
	}

	// Check removals
	for _, b := range baseline {
		if _, exists := currMap[b.Ref]; !exists {
			sb.WriteString("- ")
			sb.WriteString(formatNode(b))
			sb.WriteByte('\n')
			changedCount++
		}
	}

	if changedCount == 0 {
		return "unchanged\n", 0
	}

	return sb.String(), changedCount
}

func formatNode(n SnapNode) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s", n.Ref, n.Role)
	if n.Name != "" {
		fmt.Fprintf(&sb, " %q", n.Name)
	}
	if n.Value != "" && n.Value != n.Name {
		fmt.Fprintf(&sb, " value=%q", n.Value)
	}
	for _, p := range n.Props {
		fmt.Fprintf(&sb, " [%s]", p)
	}
	return sb.String()
}

// FilterSubtree extracts the subtree rooted at targetRef.
func FilterSubtree(nodes []SnapNode, targetRef string) []SnapNode {
	targetIdx := -1
	targetDepth := 0
	for i, n := range nodes {
		if strings.EqualFold(n.Ref, targetRef) {
			targetIdx = i
			targetDepth = n.Depth
			break
		}
	}
	if targetIdx == -1 {
		return nil
	}

	out := []SnapNode{nodes[targetIdx]}
	for i := targetIdx + 1; i < len(nodes); i++ {
		if nodes[i].Depth <= targetDepth {
			break
		}
		out = append(out, nodes[i])
	}
	return out
}

// FilterFind extracts and deterministically ranks nodes matching query text using the C1 ranker.
func FilterFind(nodes []SnapNode, query string) []SnapNode {
	q := strings.TrimSpace(query)
	if q == "" {
		return nodes
	}
	res := RankNodes(nodes, QueryParams{
		Text:  q,
		Limit: len(nodes),
	})
	nodeMap := make(map[string]SnapNode, len(nodes))
	for _, n := range nodes {
		nodeMap[n.Ref] = n
	}
	out := make([]SnapNode, 0, len(res.Items))
	for _, it := range res.Items {
		if n, ok := nodeMap[it.Ref]; ok {
			out = append(out, n)
		}
	}
	return out
}
