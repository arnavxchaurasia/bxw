package daemon

import (
	"fmt"
	"strings"
)

// DeltaChanges holds partitioned sets of added, removed, and modified nodes.
type DeltaChanges struct {
	Added    []SnapNode
	Removed  []SnapNode
	Modified [][2]SnapNode // [0] = baseline, [1] = current
}

// ComputeDelta partitions differences between baseline and current snapshots.
func ComputeDelta(baseline, current []SnapNode) DeltaChanges {
	baseMap := make(map[string]SnapNode, len(baseline))
	for _, n := range baseline {
		baseMap[n.Ref] = n
	}

	currMap := make(map[string]SnapNode, len(current))
	for _, n := range current {
		currMap[n.Ref] = n
	}

	var d DeltaChanges

	for _, c := range current {
		b, exists := baseMap[c.Ref]
		if !exists {
			d.Added = append(d.Added, c)
		} else if b.Name != c.Name || b.Value != c.Value || b.Role != c.Role || strings.Join(b.Props, ",") != strings.Join(c.Props, ",") {
			d.Modified = append(d.Modified, [2]SnapNode{b, c})
		}
	}

	for _, b := range baseline {
		if _, exists := currMap[b.Ref]; !exists {
			d.Removed = append(d.Removed, b)
		}
	}

	return d
}

// ClassifyDelta produces a concise 1-line semantic description of what changed.
func ClassifyDelta(baseline, current []SnapNode, navigated bool, targetURL, title string, dialog *Dialog) string {
	if navigated {
		if title != "" {
			return fmt.Sprintf("navigated to %s (%s)", targetURL, title)
		}
		return fmt.Sprintf("navigated to %s", targetURL)
	}

	if dialog != nil {
		if dialog.Message != "" {
			return fmt.Sprintf("dialog opened: %s (%q)", dialog.Type, dialog.Message)
		}
		return fmt.Sprintf("dialog opened: %s", dialog.Type)
	}

	delta := ComputeDelta(baseline, current)
	totalChanges := len(delta.Added) + len(delta.Removed) + len(delta.Modified)
	if totalChanges == 0 {
		return "page unchanged"
	}

	// 1. Check for modal / dialog in added nodes
	for _, n := range delta.Added {
		roleL := strings.ToLower(n.Role)
		if roleL == "dialog" || roleL == "alertdialog" || roleL == "modal" {
			// Count buttons inside added
			btnCount := 0
			for _, sub := range delta.Added {
				if strings.ToLower(sub.Role) == "button" {
					btnCount++
				}
			}
			label := n.Name
			if label == "" {
				label = "dialog"
			}
			if btnCount == 1 {
				return fmt.Sprintf("dialog opened: %s (1 button)", label)
			} else if btnCount > 1 {
				return fmt.Sprintf("dialog opened: %s (%d buttons)", label, btnCount)
			}
			return fmt.Sprintf("dialog opened: %s", label)
		}
	}

	// 2. Check for notification / toast / alert in added nodes
	for _, n := range delta.Added {
		roleL := strings.ToLower(n.Role)
		if roleL == "alert" || roleL == "status" || roleL == "banner" || strings.Contains(roleL, "toast") || strings.Contains(strings.ToLower(n.Name), "notification") {
			msg := n.Name
			if msg == "" {
				msg = n.Value
			}
			if msg != "" {
				return fmt.Sprintf("notification: %q", msg)
			}
		}
	}

	// 3. Single / few value changes
	if len(delta.Added) == 0 && len(delta.Removed) == 0 && len(delta.Modified) > 0 {
		if len(delta.Modified) == 1 {
			m := delta.Modified[0]
			oldVal := m[0].Value
			if oldVal == "" {
				oldVal = m[0].Name
			}
			newVal := m[1].Value
			if newVal == "" {
				newVal = m[1].Name
			}
			label := m[1].Name
			if label == "" {
				label = m[1].Role
			}
			if oldVal != newVal && oldVal != "" && newVal != "" {
				return fmt.Sprintf("%s %s→%s", label, oldVal, newVal)
			}
			return fmt.Sprintf("%s updated to %q", label, newVal)
		}
		if len(delta.Modified) <= 3 {
			var parts []string
			for _, m := range delta.Modified {
				oldVal := m[0].Value
				if oldVal == "" {
					oldVal = m[0].Name
				}
				newVal := m[1].Value
				if newVal == "" {
					newVal = m[1].Name
				}
				label := m[1].Name
				if label == "" {
					label = m[1].Role
				}
				if oldVal != "" && newVal != "" && oldVal != newVal {
					parts = append(parts, fmt.Sprintf("%s %s→%s", label, oldVal, newVal))
				} else {
					parts = append(parts, fmt.Sprintf("%s updated", label))
				}
			}
			return strings.Join(parts, ", ")
		}
		return fmt.Sprintf("%d fields updated", len(delta.Modified))
	}

	// 4. Region additions/removals
	btnCount := 0
	for _, a := range delta.Added {
		if strings.ToLower(a.Role) == "button" {
			btnCount++
		}
	}

	if len(delta.Added) > 0 && len(delta.Removed) == 0 && len(delta.Modified) == 0 {
		if btnCount > 0 {
			return fmt.Sprintf("+%d elements (%d buttons)", len(delta.Added), btnCount)
		}
		return fmt.Sprintf("+%d elements", len(delta.Added))
	}

	if len(delta.Removed) > 0 && len(delta.Added) == 0 && len(delta.Modified) == 0 {
		return fmt.Sprintf("-%d elements", len(delta.Removed))
	}

	// Mixed changes
	var parts []string
	if len(delta.Added) > 0 {
		parts = append(parts, fmt.Sprintf("+%d", len(delta.Added)))
	}
	if len(delta.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("-%d", len(delta.Removed)))
	}
	if len(delta.Modified) > 0 {
		parts = append(parts, fmt.Sprintf("~%d", len(delta.Modified)))
	}
	if btnCount > 0 {
		return fmt.Sprintf("%s elements (%d buttons)", strings.Join(parts, ", "), btnCount)
	}
	return fmt.Sprintf("%s elements changed", strings.Join(parts, ", "))
}
