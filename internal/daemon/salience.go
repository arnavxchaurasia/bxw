package daemon

import (
	"fmt"
	"strings"
)

// InteractiveRoles contains roles prioritized in Tier 1 (interactive elements).
var InteractiveRoles = map[string]bool{
	"button":           true,
	"link":             true,
	"textbox":          true,
	"textarea":         true,
	"combobox":         true,
	"listbox":          true,
	"checkbox":         true,
	"radio":            true,
	"switch":           true,
	"slider":           true,
	"spinbutton":       true,
	"menuitem":         true,
	"menuitemcheckbox": true,
	"tab":              true,
	"searchbox":        true,
	"option":           true,
}

// StructureRoles contains roles prioritized in Tier 2 (structure & landmarks).
var StructureRoles = map[string]bool{
	"heading":       true,
	"navigation":    true,
	"main":          true,
	"banner":        true,
	"contentinfo":   true,
	"complementary": true,
	"region":        true,
	"form":          true,
	"search":        true,
}

// NodeSalienceTier returns the salience tier (1: interactive, 2: structure, 3: text/other).
func NodeSalienceTier(role string) int {
	r := strings.ToLower(strings.TrimSpace(role))
	if InteractiveRoles[r] {
		return 1
	}
	if StructureRoles[r] {
		return 2
	}
	return 3
}

// NodeTokenEstimate estimates the token cost of a rendered node line using the char/4 heuristic.
func NodeTokenEstimate(n SnapNode) int {
	line := formatSnapshotLine(n)
	cost := len(line) / 4
	if cost < 1 {
		cost = 1
	}
	return cost
}

func formatSnapshotLine(n SnapNode) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("  ", n.Depth))
	fmt.Fprintf(&b, "%s %s", n.Ref, n.Role)
	if n.Name != "" {
		fmt.Fprintf(&b, " %q", n.Name)
	}
	if n.Value != "" && n.Value != n.Name {
		fmt.Fprintf(&b, " value=%q", n.Value)
	}
	for _, p := range n.Props {
		fmt.Fprintf(&b, " [%s]", p)
	}
	b.WriteByte('\n')
	return b.String()
}

// BudgetSnapshot keeps the most salient nodes within an estimated token budget,
// in document order, and reports how many descendants were pruned under each kept
// node (keyed by that node's ref) so the caller can mark drill-down points.
func BudgetSnapshot(nodes []SnapNode, budget int) (kept []SnapNode, elided map[string]int) {
	if budget <= 0 || len(nodes) == 0 {
		return nodes, nil
	}

	totalTokens := 0
	nodeCosts := make([]int, len(nodes))
	for i, n := range nodes {
		c := NodeTokenEstimate(n)
		nodeCosts[i] = c
		totalTokens += c
	}

	// If the entire snapshot fits comfortably within budget, return as-is
	if totalTokens <= budget {
		return nodes, nil
	}

	// Find the nearest ancestor index for each node based on Depth
	ancestor := make([]int, len(nodes))
	for i := range ancestor {
		ancestor[i] = -1
	}
	var stack []int // stack of node indices
	for i, n := range nodes {
		for len(stack) > 0 && nodes[stack[len(stack)-1]].Depth >= n.Depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			ancestor[i] = stack[len(stack)-1]
		}
		stack = append(stack, i)
	}

	selected := make(map[int]bool, len(nodes))
	runningTokens := 0

	// Helper to select a node and its ancestors
	selectWithAncestors := func(idx int) bool {
		var toAdd []int
		for curr := idx; curr != -1; curr = ancestor[curr] {
			if selected[curr] {
				break
			}
			toAdd = append(toAdd, curr)
		}
		additionalCost := 0
		for _, a := range toAdd {
			additionalCost += nodeCosts[a]
		}
		if len(selected) > 0 && runningTokens+additionalCost > budget {
			return false
		}
		for _, a := range toAdd {
			selected[a] = true
			runningTokens += nodeCosts[a]
		}
		return true
	}

	// Tier 1: Interactive
	for i, n := range nodes {
		if NodeSalienceTier(n.Role) == 1 {
			selectWithAncestors(i)
		}
	}

	// Tier 2: Structure
	for i, n := range nodes {
		if NodeSalienceTier(n.Role) == 2 {
			selectWithAncestors(i)
		}
	}

	// Tier 3: Text / others
	for i, n := range nodes {
		if NodeSalienceTier(n.Role) == 3 {
			selectWithAncestors(i)
		}
	}

	// Ensure at least the root node (first node) is included if nothing fit
	if len(selected) == 0 && len(nodes) > 0 {
		selected[0] = true
	}

	// Build kept slice in original document order
	kept = make([]SnapNode, 0, len(selected))
	elided = make(map[string]int)

	for i, n := range nodes {
		if selected[i] {
			kept = append(kept, n)
		} else {
			// Find nearest kept ancestor to attribute elided count
			for a := ancestor[i]; a != -1; a = ancestor[a] {
				if selected[a] {
					elided[nodes[a].Ref]++
					break
				}
			}
		}
	}

	return kept, elided
}
