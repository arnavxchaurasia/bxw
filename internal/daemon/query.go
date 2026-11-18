package daemon

import (
	"fmt"
	"sort"
	"strings"
)

// QueryParams specifies search criteria to filter and rank accessibility nodes.
type QueryParams struct {
	Role  string `json:"role,omitempty"`  // ARIA role filter (e.g. "button", "textbox")
	Name  string `json:"name,omitempty"`  // Accessible name (exact or substring)
	Text  string `json:"text,omitempty"`  // General text query (matches name or value)
	Near  string `json:"near,omitempty"`  // Label or text of nearby element to bias ranking
	Limit int    `json:"limit,omitempty"` // Maximum number of results (0 = default 10)
}

// QueryItem represents a single ranked search result with a 1-line context string.
type QueryItem struct {
	Ref     string   `json:"ref"`
	Role    string   `json:"role"`
	Name    string   `json:"name,omitempty"`
	Value   string   `json:"value,omitempty"`
	Props   []string `json:"props,omitempty"`
	Context string   `json:"context"` // 1-line context representation
	Score   int      `json:"score"`
}

// QueryResult contains the ranked matches and metadata.
type QueryResult struct {
	URL   string      `json:"url,omitempty"`
	Title string      `json:"title,omitempty"`
	Total int         `json:"total"`
	Items []QueryItem `json:"items"`
}

// Text formats the query result as human-readable 1-line items.
func (r QueryResult) Text() string {
	if len(r.Items) == 0 {
		return "no matching elements found\n"
	}
	var b strings.Builder
	for _, it := range r.Items {
		b.WriteString(it.Context)
		b.WriteByte('\n')
	}
	return b.String()
}

type scoredItem struct {
	item QueryItem
	idx  int
}

// RankNodes filters and deterministically ranks accessibility nodes by query criteria.
// Ranking hierarchy: exact role+name > name contains > text match > proximity to --near.
func RankNodes(nodes []SnapNode, params QueryParams) QueryResult {
	roleQ := strings.ToLower(strings.TrimSpace(params.Role))
	nameQ := strings.ToLower(strings.TrimSpace(params.Name))
	textQ := strings.ToLower(strings.TrimSpace(params.Text))
	nearQ := strings.ToLower(strings.TrimSpace(params.Near))

	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}

	// 1. Identify indices of near targets
	var nearIndices []int
	if nearQ != "" {
		for i, n := range nodes {
			nameL := strings.ToLower(n.Name)
			valL := strings.ToLower(n.Value)
			if strings.Contains(nameL, nearQ) || strings.Contains(valL, nearQ) {
				nearIndices = append(nearIndices, i)
			}
		}
	}

	var candidates []scoredItem

	for i, n := range nodes {
		roleL := strings.ToLower(n.Role)
		nameL := strings.ToLower(n.Name)
		valL := strings.ToLower(n.Value)

		// Filter by role if specified
		if roleQ != "" && roleL != roleQ {
			continue
		}

		// Filter by name if specified
		if nameQ != "" && !strings.Contains(nameL, nameQ) {
			continue
		}

		// Filter by text if specified (matches name, value, or role)
		if textQ != "" && !strings.Contains(nameL, textQ) && !strings.Contains(valL, textQ) && !strings.Contains(roleL, textQ) {
			continue
		}

		// Calculate score
		score := 0

		// Role bonus
		if roleQ != "" && roleL == roleQ {
			score += 10000
		}

		// Name matching tier
		if nameQ != "" {
			if roleQ != "" && roleL == roleQ && nameL == nameQ {
				score += 1000000 // exact role + exact name
			} else if nameL == nameQ {
				score += 500000 // exact name
			} else if strings.HasPrefix(nameL, nameQ) {
				score += 300000 // name prefix
			} else {
				score += 200000 // name substring
			}
		}

		// Text matching tier
		if textQ != "" {
			if nameL == textQ {
				score += 100000
			} else if strings.HasPrefix(nameL, textQ) {
				score += 70000
			} else if strings.Contains(nameL, textQ) {
				score += 50000
			}
			if valL == textQ {
				score += 40000
			} else if strings.Contains(valL, textQ) {
				score += 20000
			}
			if roleL == textQ {
				score += 30000
			} else if strings.Contains(roleL, textQ) {
				score += 15000
			}
		}

		// Proximity to --near
		if len(nearIndices) > 0 {
			minDist := len(nodes) + 1
			closestNearIdx := -1
			for _, nj := range nearIndices {
				d := i - nj
				if d < 0 {
					d = -d
				}
				if d < minDist {
					minDist = d
					closestNearIdx = nj
				}
			}
			if minDist <= 100 {
				prox := 10000 - minDist*100
				if prox < 0 {
					prox = 0
				}
				// Forward in DOM order (following the near label) gets slight tie-break advantage
				if i > closestNearIdx {
					prox += 50
				}
				score += prox
			}
		}

		// 1-line context
		var ctxB strings.Builder
		fmt.Fprintf(&ctxB, "%s %s", n.Ref, n.Role)
		if n.Name != "" {
			fmt.Fprintf(&ctxB, " %q", n.Name)
		}
		if n.Value != "" && n.Value != n.Name {
			fmt.Fprintf(&ctxB, " value=%q", n.Value)
		}
		for _, p := range n.Props {
			fmt.Fprintf(&ctxB, " [%s]", p)
		}

		candidates = append(candidates, scoredItem{
			item: QueryItem{
				Ref:     n.Ref,
				Role:    n.Role,
				Name:    n.Name,
				Value:   n.Value,
				Props:   n.Props,
				Context: ctxB.String(),
				Score:   score,
			},
			idx: i,
		})
	}

	// Deterministic sort: score DESC, then original index ASC
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].item.Score != candidates[j].item.Score {
			return candidates[i].item.Score > candidates[j].item.Score
		}
		return candidates[i].idx < candidates[j].idx
	})

	total := len(candidates)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	items := make([]QueryItem, len(candidates))
	for i, c := range candidates {
		items[i] = c.item
	}

	return QueryResult{
		Total: total,
		Items: items,
	}
}
