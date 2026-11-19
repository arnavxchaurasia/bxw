package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/browser"
)

func sampleNodes() []SnapNode {
	return []SnapNode{
		{Ref: "e1", Depth: 0, Role: "heading", Name: "Checkout Summary"},
		{Ref: "e2", Depth: 1, Role: "StaticText", Name: "Items (2)"},
		{Ref: "e3", Depth: 1, Role: "button", Name: "Cancel"},
		{Ref: "e4", Depth: 1, Role: "StaticText", Name: "Total: $149.99"},
		{Ref: "e5", Depth: 1, Role: "button", Name: "Checkout"},
		{Ref: "e6", Depth: 1, Role: "button", Name: "Checkout with PayPal"},
		{Ref: "e7", Depth: 1, Role: "textbox", Name: "Promo Code", Value: "SUMMER20"},
		{Ref: "e8", Depth: 1, Role: "button", Name: "Apply Promo"},
		{Ref: "e9", Depth: 1, Role: "link", Name: "Help & FAQ"},
	}
}

func TestC1RankedRetrieval(t *testing.T) {
	nodes := sampleNodes()

	// 1. Acceptance test from specification:
	// --role button --near "Total" returns checkout button first; result ranked & bounded
	t.Run("NearTotalReturnsCheckoutButtonFirst", func(t *testing.T) {
		res := RankNodes(nodes, QueryParams{
			Role:  "button",
			Near:  "Total",
			Limit: 3,
		})

		if len(res.Items) != 3 {
			t.Fatalf("expected 3 bounded items, got %d", len(res.Items))
		}
		if res.Total < 4 {
			t.Fatalf("expected at least 4 total matching buttons, got %d", res.Total)
		}

		// e5 is "Checkout" immediately following e4 ("Total: $149.99")
		first := res.Items[0]
		if first.Ref != "e5" || first.Name != "Checkout" {
			t.Fatalf("expected first item to be Checkout (e5), got %s (%s)", first.Ref, first.Name)
		}

		// e3 is "Cancel" at distance 1 before Total
		// e6 is "Checkout with PayPal" at distance 2
		if res.Items[1].Ref != "e3" && res.Items[1].Ref != "e6" {
			t.Errorf("expected second item to be Cancel (e3) or Checkout with PayPal (e6), got %s", res.Items[1].Ref)
		}
	})

	// 2. Exact role + name > name contains
	t.Run("ExactRoleAndNameBeatsPartial", func(t *testing.T) {
		res := RankNodes(nodes, QueryParams{
			Role: "button",
			Name: "Checkout",
		})
		if len(res.Items) == 0 {
			t.Fatal("expected matches, got none")
		}
		if res.Items[0].Ref != "e5" || res.Items[0].Name != "Checkout" {
			t.Fatalf("expected exact match e5 first, got %+v", res.Items[0])
		}
		// e6 ("Checkout with PayPal") should be ranked second
		if len(res.Items) < 2 || res.Items[1].Ref != "e6" {
			t.Fatalf("expected e6 second, got %+v", res.Items)
		}
		if res.Items[0].Score <= res.Items[1].Score {
			t.Errorf("expected exact match score (%d) > partial match score (%d)",
				res.Items[0].Score, res.Items[1].Score)
		}
	})

	// 3. Name contains > text match in value
	t.Run("NameContainsBeatsValueMatch", func(t *testing.T) {
		res := RankNodes(nodes, QueryParams{
			Text: "Promo",
		})
		if len(res.Items) < 2 {
			t.Fatalf("expected at least 2 matches for 'Promo', got %d", len(res.Items))
		}
		// e7 is textbox "Promo Code" (name contains)
		// e8 is button "Apply Promo" (name contains)
		for _, it := range res.Items[:2] {
			if !strings.Contains(it.Name, "Promo") {
				t.Errorf("expected name to contain 'Promo', got %q", it.Name)
			}
		}
	})

	// 4. Bounded output limit
	t.Run("BoundedOutputHonorsLimit", func(t *testing.T) {
		res := RankNodes(nodes, QueryParams{
			Limit: 2,
		})
		if len(res.Items) != 2 {
			t.Fatalf("expected exactly 2 items, got %d", len(res.Items))
		}
		if res.Total != len(nodes) {
			t.Fatalf("expected Total %d, got %d", len(nodes), res.Total)
		}
	})

	// 5. Deterministic ordering: identical scores preserve stable index order
	t.Run("DeterministicOrder", func(t *testing.T) {
		res1 := RankNodes(nodes, QueryParams{Role: "button"})
		res2 := RankNodes(nodes, QueryParams{Role: "button"})
		if len(res1.Items) != len(res2.Items) {
			t.Fatalf("non-deterministic lengths: %d vs %d", len(res1.Items), len(res2.Items))
		}
		for i := range res1.Items {
			if res1.Items[i].Ref != res2.Items[i].Ref {
				t.Errorf("index %d differs: %s vs %s", i, res1.Items[i].Ref, res2.Items[i].Ref)
			}
		}
	})

	// 6. Text output format: 1-line context per match
	t.Run("TextOutputFormat", func(t *testing.T) {
		res := RankNodes(nodes, QueryParams{
			Role:  "button",
			Name:  "Checkout",
			Limit: 1,
		})
		txt := res.Text()
		expected := "e5 button \"Checkout\"\n"
		if txt != expected {
			t.Errorf("expected %q, got %q", expected, txt)
		}
	})
}

func TestC1QueryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live browser query test in short mode")
	}

	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no supported Chromium browser found on this machine")
	}

	targetBrowser := bs[0]

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Cart</title></head>
<body>
  <h1>Order Summary</h1>
  <button id="cancel">Cancel Order</button>
  <p>Subtotal: $80.00</p>
  <p>Tax: $10.00</p>
  <p id="total">Total: $90.00</p>
  <button id="checkout">Proceed to Checkout</button>
  <br>
  <button id="help">Need Help?</button>
</body>
</html>`))
	}))
	defer ts.Close()

	d := &Daemon{
		br:        targetBrowser,
		stop:      make(chan struct{}),
		since:     time.Now(),
		sessions:  make(map[string]*tabSession),
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	if err := d.launch(true, ts.URL, false); err != nil {
		t.Fatalf("launch headless %s: %v", targetBrowser.Name, err)
	}
	defer func() {
		if d.agentPID != 0 {
			if proc, err := os.FindProcess(d.agentPID); err == nil {
				_ = proc.Kill()
				_, _ = proc.Wait()
			}
		}
	}()

	var tabID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tabs, err := d.listTabs()
		if err == nil && len(tabs) > 0 {
			for _, tab := range tabs {
				if strings.HasPrefix(tab.URL, ts.URL) {
					tabID = tab.ID
					break
				}
			}
			if tabID == "" && len(tabs) > 0 {
				tabID = tabs[0].ID
			}
		}
		if tabID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tabID == "" {
		t.Fatal("no tab found")
	}

	// Ensure page is loaded
	if _, err := d.wait(tabID, waitCond{Selector: "button#checkout"}, 10*time.Second); err != nil {
		if _, navErr := d.gotoURL(tabID, ts.URL, 10*time.Second); navErr != nil {
			t.Fatalf("goto: %v", navErr)
		}
		if _, err := d.wait(tabID, waitCond{Selector: "button#checkout"}, 10*time.Second); err != nil {
			t.Fatalf("wait for button#checkout: %v", err)
		}
	}

	res, err := d.query(tabID, QueryParams{
		Role:  "button",
		Near:  "Total",
		Limit: 2,
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(res.Items) == 0 {
		t.Fatal("expected matching query items, got none")
	}

	first := res.Items[0]
	if !strings.Contains(first.Name, "Checkout") {
		t.Fatalf("expected Checkout button first near 'Total', got %q (%+v)", first.Name, first)
	}
}
