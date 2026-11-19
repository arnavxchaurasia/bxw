package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"

	"bxw/internal/browser"
	"bxw/internal/exit"
)

type waitCond struct {
	Selector string
	Text     string
	Load     bool
	MS       int
}

func (d *Daemon) gotoURL(tabID, targetURL string, timeout time.Duration) (ActResult, error) {
	if err := browser.CheckURL(targetURL); err != nil {
		return ActResult{}, err
	}

	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		_, err := cdp.Call(ctx, sess.conn, page.Navigate, page.NavigateParams{URL: targetURL})
		return err
	})
}

func (d *Daemon) wait(tabID string, cond waitCond, timeout time.Duration) (map[string]any, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if cond.MS > 0 {
		time.Sleep(time.Duration(cond.MS) * time.Millisecond)
		return map[string]any{"satisfied": true, "waitedMs": cond.MS}, nil
	}

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	what := "condition"
	if cond.Selector != "" {
		what = fmt.Sprintf("selector %q", cond.Selector)
	} else if cond.Text != "" {
		what = fmt.Sprintf("text %q", cond.Text)
	} else if cond.Load {
		what = "page load"
	}

	for {
		if err := sess.errIfDialog(); err != nil {
			return nil, err
		}

		var ok bool
		var checkErr error

		if cond.Selector != "" {
			sel, err := sess.selector(ctx, cond.Selector)
			if err != nil {
				sel = cond.Selector
			}
			checkErr = sess.eval(ctx, fmt.Sprintf("(function(){ try { return !!document.querySelector(%q); } catch(e) { return false; } })()", sel), &ok)
		} else if cond.Text != "" {
			b, _ := json.Marshal(cond.Text)
			checkErr = sess.eval(ctx, fmt.Sprintf("(function(){ try { return document.body ? document.body.innerText.includes(%s) : false; } catch(e) { return false; } })()", string(b)), &ok)
		} else if cond.Load {
			checkErr = sess.eval(ctx, "(function(){ try { return document.readyState === 'complete'; } catch(e) { return false; } })()", &ok)
		}

		if checkErr == nil && ok {
			elapsed := time.Since(start).Milliseconds()
			return map[string]any{"satisfied": true, "waitedMs": elapsed}, nil
		}

		select {
		case <-ctx.Done():
			return nil, exit.New(exit.User, "timed out after %s waiting for %s", timeout, what)
		case <-ticker.C:
		}
	}
}
