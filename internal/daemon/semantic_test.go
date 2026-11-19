package daemon

import (
	"strings"
	"testing"
)

func TestC2SemanticActionDeltas(t *testing.T) {
	tests := []struct {
		name       string
		baseline   []SnapNode
		current    []SnapNode
		navigated  bool
		targetURL  string
		title      string
		dialog     *Dialog
		wantPrefix string
		wantExact  string
	}{
		{
			name:      "NavigatedToURL",
			navigated: true,
			targetURL: "https://example.com/checkout",
			title:     "Checkout",
			wantExact: "navigated to https://example.com/checkout (Checkout)",
		},
		{
			name: "NativeDialogOpened",
			dialog: &Dialog{
				Type:    "confirm",
				Message: "Are you sure?",
			},
			wantExact: `dialog opened: confirm ("Are you sure?")`,
		},
		{
			name:      "PageUnchanged",
			baseline:  []SnapNode{{Ref: "e1", Role: "button", Name: "Click"}},
			current:   []SnapNode{{Ref: "e1", Role: "button", Name: "Click"}},
			wantExact: "page unchanged",
		},
		{
			name: "DOMDialogOpenedWithButtons",
			baseline: []SnapNode{
				{Ref: "e1", Role: "button", Name: "Open Modal"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "button", Name: "Open Modal"},
				{Ref: "e2", Role: "dialog", Name: "Confirm Order"},
				{Ref: "e3", Role: "button", Name: "Cancel"},
				{Ref: "e4", Role: "button", Name: "Confirm"},
			},
			wantExact: "dialog opened: Confirm Order (2 buttons)",
		},
		{
			name: "NotificationOrToastAdded",
			baseline: []SnapNode{
				{Ref: "e1", Role: "button", Name: "Save"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "button", Name: "Save"},
				{Ref: "e2", Role: "alert", Name: "Item added to cart"},
			},
			wantExact: `notification: "Item added to cart"`,
		},
		{
			name: "SingleValueChange",
			baseline: []SnapNode{
				{Ref: "e1", Role: "textbox", Name: "Email", Value: "old@example.com"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "textbox", Name: "Email", Value: "new@example.com"},
			},
			wantExact: "Email old@example.com→new@example.com",
		},
		{
			name: "BadgeOrCounterChange",
			baseline: []SnapNode{
				{Ref: "e1", Role: "generic", Name: "cart badge", Value: "1"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "generic", Name: "cart badge", Value: "2"},
			},
			wantExact: "cart badge 1→2",
		},
		{
			name: "MultipleValueChanges",
			baseline: []SnapNode{
				{Ref: "e1", Role: "textbox", Name: "First", Value: "Alice"},
				{Ref: "e2", Role: "textbox", Name: "Last", Value: "Smith"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "textbox", Name: "First", Value: "Bob"},
				{Ref: "e2", Role: "textbox", Name: "Last", Value: "Jones"},
			},
			wantExact: "First Alice→Bob, Last Smith→Jones",
		},
		{
			name: "RegionAddedWithButtons",
			baseline: []SnapNode{
				{Ref: "e1", Role: "heading", Name: "Home"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "heading", Name: "Home"},
				{Ref: "e2", Role: "region", Name: "Toolbar"},
				{Ref: "e3", Role: "button", Name: "Play"},
				{Ref: "e4", Role: "button", Name: "Pause"},
			},
			wantExact: "+3 elements (2 buttons)",
		},
		{
			name: "RegionRemoved",
			baseline: []SnapNode{
				{Ref: "e1", Role: "heading", Name: "Home"},
				{Ref: "e2", Role: "banner", Name: "Promo"},
				{Ref: "e3", Role: "text", Name: "Sale today"},
			},
			current: []SnapNode{
				{Ref: "e1", Role: "heading", Name: "Home"},
			},
			wantExact: "-2 elements",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyDelta(tt.baseline, tt.current, tt.navigated, tt.targetURL, tt.title, tt.dialog)
			if tt.wantExact != "" && got != tt.wantExact {
				t.Errorf("ClassifyDelta() = %q, want %q", got, tt.wantExact)
			}
			if tt.wantPrefix != "" && !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("ClassifyDelta() = %q, want prefix %q", got, tt.wantPrefix)
			}
		})
	}
}

func TestC2ActResultTerseSummary(t *testing.T) {
	r := ActResult{
		Delta: "cart badge 1→2",
	}
	summary := r.TerseSummary("click e3")
	if summary != "click e3 -> cart badge 1→2" {
		t.Errorf("unexpected TerseSummary: %q", summary)
	}

	rNav := ActResult{
		Navigated: true,
		URL:       "https://example.com/checkout",
		Title:     "Checkout",
		Delta:     "navigated to https://example.com/checkout (Checkout)",
	}
	summaryNav := rNav.TerseSummary("goto")
	if summaryNav != "goto -> navigated to https://example.com/checkout (Checkout)" {
		t.Errorf("unexpected TerseSummary: %q", summaryNav)
	}
}
