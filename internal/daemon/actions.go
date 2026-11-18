package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"

	"bxw/internal/config"
	"bxw/internal/contextmgr"
	"bxw/internal/exit"
	"bxw/internal/history"
)

// ActResult describes what an action performed and whether navigation or dialogs occurred.
type ActResult struct {
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Navigated    bool     `json:"navigated"`
	NewTabs      []string `json:"new_tabs,omitempty"`
	Dialog       *Dialog  `json:"dialog,omitempty"`
	ChangedNodes int      `json:"changed_nodes,omitempty"`
	Terse        string   `json:"terse,omitempty"`
}

func (r ActResult) TerseSummary(action string) string {
	if r.Navigated {
		return fmt.Sprintf("%s -> navigated to %s (%s)", action, r.URL, r.Title)
	}
	if r.Dialog != nil {
		return fmt.Sprintf("%s -> dialog: %s (%q)", action, r.Dialog.Type, r.Dialog.Message)
	}
	if r.ChangedNodes > 0 {
		return fmt.Sprintf("%s -> %d nodes changed", action, r.ChangedNodes)
	}
	return fmt.Sprintf("%s -> done", action)
}

func (d *Daemon) actOn(tabID string, timeout time.Duration, fn func(ctx context.Context, sess *tabSession) error) (ActResult, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return ActResult{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if err := sess.errIfDialog(); err != nil {
		return ActResult{}, err
	}

	actx, interrupt := context.WithCancelCause(context.Background())
	defer interrupt(nil)

	sess.dmu.Lock()
	sess.interrupt = interrupt
	sess.dmu.Unlock()
	defer func() {
		sess.dmu.Lock()
		sess.interrupt = nil
		sess.dmu.Unlock()
	}()

	loader := sess.loaderID(actx)
	var started atomic.Bool
	frameStarts := cdp.Events(actx, sess.conn, page.FrameStartedLoading)
	go func() {
		for _, err := range frameStarts {
			if err != nil {
				return
			}
			started.Store(true)
		}
	}()

	ctx, cancel := context.WithTimeout(actx, timeout)
	defer cancel()

	err = fn(ctx, sess)
	if errors.Is(context.Cause(actx), errDialog) {
		return ActResult{Dialog: sess.pendingDialog()}, nil
	}
	if err != nil {
		return ActResult{}, err
	}

	sess.settle(actx, &started)
	if errors.Is(context.Cause(actx), errDialog) {
		return ActResult{Dialog: sess.pendingDialog()}, nil
	}

	r := ActResult{}
	r.Navigated = sess.loaderID(actx) != loader
	r.URL, r.Title = sess.location(actx)

	// Follow mode: bring tab to front if enabled
	if config.IsFollowEnabled() {
		_, _ = cdp.Call(ctx, sess.conn, page.BringToFront, cdp.Empty{})
	}

	// Check post-action node changes against stored baseline
	if base, err := LoadTabState(d.br.Name, tabID); err == nil && len(base.Nodes) > 0 {
		tree, err := cdp.Call(ctx, sess.conn, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
		if err == nil {
			nodes, refs, descs := buildSnapshot(tree.Nodes)
			sess.refs = refs
			sess.refDescs = descs
			_, changedCount := DiffSnapshots(base.Nodes, nodes)
			r.ChangedNodes = changedCount
			_ = SaveTabState(d.br.Name, tabID, Snapshot{URL: r.URL, Title: r.Title, Nodes: nodes, Hash: ComputeHash(nodes)}, refs)
		}
	}

	if r.Navigated {
		_ = history.Log(d.br.Name, "Default", tabID, r.URL, r.Title, "agent")
	}

	// F2: Record action in durable session model and append dated note
	summaryText := r.TerseSummary("act")
	_ = contextmgr.RecordAction(d.br.Name, "default", tabID, "action", summaryText, "", "")

	return r, nil
}

// F5 threshold constants: when mutations exceed these ratios, snapshot returns wholesale replacement.
const (
	WholesaleThresholdChangedRatio = 0.70 // Changed nodes >= 70% of baseline
	WholesaleThresholdTextLenRatio = 0.85 // Diff text length >= 85% of full snapshot text
)

func (d *Daemon) snapshot(tabID string, full bool, scopeRef, findQuery string, timeout time.Duration) (Snapshot, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return Snapshot{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if err := sess.errIfDialog(); err != nil {
		return Snapshot{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tree, err := cdp.Call(ctx, sess.conn, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return Snapshot{}, exit.New(exit.Upstream, "accessibility getFullAXTree: %v", err)
	}

	nodes, refs, descs := buildSnapshot(tree.Nodes)
	sess.refs = refs
	sess.refDescs = descs
	sess.refLoader = sess.loaderID(ctx)

	url, title := sess.location(ctx)
	currHash := ComputeHash(nodes)

	// Scoped subtree or find filter
	filteredNodes := nodes
	if scopeRef != "" {
		filteredNodes = FilterSubtree(nodes, scopeRef)
	} else if findQuery != "" {
		filteredNodes = FilterFind(nodes, findQuery)
	}

	snap := Snapshot{
		URL:   url,
		Title: title,
		Nodes: filteredNodes,
		Hash:  currHash,
	}

	// Diff snapshots against stored baseline on disk (Pillar B)
	base, err := LoadTabState(d.br.Name, tabID)
	hasBaseline := (err == nil && len(base.Nodes) > 0)

	if !full && hasBaseline && scopeRef == "" && findQuery == "" {
		diff, changedCount := DiffSnapshots(base.Nodes, nodes)
		if diff == "unchanged\n" {
			snap.Unchanged = true
			snap.Diff = diff
		} else {
			// F5: If the diff degenerates to ~full page replacement (changed >= 70% of baseline,
			// or diff length >= 85% of full snapshot text), return full snapshot with note and reset baseline
			fullTextLen := len(Snapshot{URL: url, Title: title, Nodes: nodes}.Text())
			isWholesale := (float64(changedCount) >= WholesaleThresholdChangedRatio*float64(len(base.Nodes))) ||
				(fullTextLen > 0 && float64(len(diff)) >= WholesaleThresholdTextLenRatio*float64(fullTextLen))

			if isWholesale {
				snap.PageReplaced = true
				snap.Diff = "" // Clear diff so full snapshot formats with [page replaced — full snapshot]
			} else {
				snap.Diff = diff
			}
		}
	}

	// Save baseline to disk: state/<browser>/<tab>.json
	_ = SaveTabState(d.br.Name, tabID, Snapshot{URL: url, Title: title, Nodes: nodes, Hash: currHash}, refs)

	// F2: Update structured machine state with latest snapshot digest
	if sc, err := contextmgr.LoadSession("default"); err == nil {
		sc.Browser = d.br.Name
		sc.ActiveTab = tabID
		sc.URL = url
		sc.Title = title
		sc.LastSnapshot = currHash
		_ = contextmgr.SaveSession(sc)
	}

	return snap, nil
}

func (d *Daemon) query(tabID string, params QueryParams, timeout time.Duration) (QueryResult, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return QueryResult{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if err := sess.errIfDialog(); err != nil {
		return QueryResult{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tree, err := cdp.Call(ctx, sess.conn, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return QueryResult{}, exit.New(exit.Upstream, "accessibility getFullAXTree: %v", err)
	}

	nodes, refs, descs := buildSnapshot(tree.Nodes)
	sess.refs = refs
	sess.refDescs = descs
	sess.refLoader = sess.loaderID(ctx)

	url, title := sess.location(ctx)
	res := RankNodes(nodes, params)
	res.URL = url
	res.Title = title
	return res, nil
}

func center(ctx context.Context, sess *tabSession, el string, timeout time.Duration) (float64, float64, error) {
	sel, err := sess.selector(ctx, el)
	if err != nil {
		return 0, 0, err
	}

	q, _ := json.Marshal(sel)
	script := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return null;
		el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
		const r = el.getBoundingClientRect();
		return [r.x + r.width / 2, r.y + r.height / 2];
	})()`, q)

	var xy []float64
	if err := sess.eval(ctx, script, &xy); err != nil {
		return 0, 0, exit.New(exit.Upstream, "locate %s: %v", el, err)
	}
	if len(xy) != 2 {
		return 0, 0, exit.New(exit.User, "no visible element matches %q (waited %s)", el, timeout)
	}
	return xy[0], xy[1], nil
}

func moveMouse(ctx context.Context, sess *tabSession, el string, timeout time.Duration) (float64, float64, error) {
	x, y, err := center(ctx, sess, el, timeout)
	if err != nil {
		return 0, 0, err
	}

	_, err = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
		Type: input.DispatchMouseEventTypeMouseMoved,
		X:    x,
		Y:    y,
	})
	if err != nil {
		return 0, 0, err
	}
	return x, y, nil
}

func (d *Daemon) hover(tabID, el string, timeout time.Duration) (ActResult, error) {
	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		_, _, err := moveMouse(ctx, sess, el, timeout)
		return err
	})
}

func (d *Daemon) drag(tabID, from, to string, timeout time.Duration) (ActResult, error) {
	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		fx, fy, err := center(ctx, sess, from, timeout)
		if err != nil {
			return err
		}
		tx, ty, err := center(ctx, sess, to, timeout)
		if err != nil {
			return err
		}

		// 1. Move to source
		if _, err := cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type: input.DispatchMouseEventTypeMouseMoved,
			X:    fx,
			Y:    fy,
		}); err != nil {
			return err
		}

		// 2. Mouse press
		if _, err := cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMousePressed,
			Button:     input.MouseButtonLeft,
			Buttons:    1,
			ClickCount: 1,
			X:          fx,
			Y:          fy,
		}); err != nil {
			return err
		}

		// 3. Move halfway (triggers dragover/HTML5 DnD)
		if _, err := cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:    input.DispatchMouseEventTypeMouseMoved,
			Buttons: 1,
			X:       (fx + tx) / 2,
			Y:       (fy + ty) / 2,
		}); err != nil {
			return err
		}

		// 4. Move to destination
		if _, err := cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:    input.DispatchMouseEventTypeMouseMoved,
			Buttons: 1,
			X:       tx,
			Y:       ty,
		}); err != nil {
			return err
		}

		// 5. Release mouse
		if _, err := cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMouseReleased,
			Button:     input.MouseButtonLeft,
			Buttons:    0,
			ClickCount: 1,
			X:          tx,
			Y:          ty,
		}); err != nil {
			return err
		}

		return nil
	})
}

func (d *Daemon) upload(tabID, el string, paths []string, timeout time.Duration) (ActResult, error) {
	if len(paths) == 0 {
		return ActResult{}, exit.New(exit.Validation, "upload requires at least one file path")
	}

	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		if refPattern.MatchString(el) {
			backend, ok := sess.refs[el]
			if !ok || (sess.refLoader != "" && sess.loaderID(ctx) != sess.refLoader) {
				return exit.New(exit.User, "%s is not in this tab's latest snapshot: run bxw snapshot %s again", el, sess.id)
			}
			_, err := cdp.Call(ctx, sess.conn, dom.SetFileInputFiles, dom.SetFileInputFilesParams{
				Files:         paths,
				BackendNodeID: backend,
			})
			return err
		}

		doc, err := cdp.Call(ctx, sess.conn, dom.GetDocument, dom.GetDocumentParams{})
		if err != nil || doc.Root == nil {
			return exit.New(exit.Upstream, "get document: %v", err)
		}
		res, err := cdp.Call(ctx, sess.conn, dom.QuerySelector, dom.QuerySelectorParams{
			NodeID:   doc.Root.NodeID,
			Selector: el,
		})
		if err != nil || res.NodeID == 0 {
			return exit.New(exit.User, "no element matches selector %q", el)
		}

		_, err = cdp.Call(ctx, sess.conn, dom.SetFileInputFiles, dom.SetFileInputFilesParams{
			Files:  paths,
			NodeID: res.NodeID,
		})
		return err
	})
}

func (d *Daemon) click(tabID, el string, double bool, timeout time.Duration) (ActResult, error) {
	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		x, y, err := moveMouse(ctx, sess, el, timeout)
		if err != nil {
			return err
		}

		// Mouse press
		_, err = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMousePressed,
			Button:     input.MouseButtonLeft,
			Buttons:    1,
			ClickCount: 1,
			X:          x,
			Y:          y,
		})
		if err != nil {
			return err
		}

		// Mouse release
		_, err = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMouseReleased,
			Button:     input.MouseButtonLeft,
			Buttons:    0,
			ClickCount: 1,
			X:          x,
			Y:          y,
		})
		if err != nil {
			return err
		}

		if double {
			// Second click
			_, _ = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
				Type:       input.DispatchMouseEventTypeMousePressed,
				Button:     input.MouseButtonLeft,
				Buttons:    1,
				ClickCount: 2,
				X:          x,
				Y:          y,
			})
			_, _ = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
				Type:       input.DispatchMouseEventTypeMouseReleased,
				Button:     input.MouseButtonLeft,
				Buttons:    0,
				ClickCount: 2,
				X:          x,
				Y:          y,
			})
		}

		return nil
	})
}

func (d *Daemon) fill(tabID string, pairs [][2]string, timeout time.Duration) (ActResult, error) {
	if len(pairs) == 0 {
		return ActResult{}, exit.New(exit.Validation, "fill requires an element and a value")
	}
	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		for _, p := range pairs {
			if err := fillOne(ctx, sess, p[0], p[1], timeout); err != nil {
				return err
			}
		}
		return nil
	})
}

const fieldKindJS = `(el => {
	if (!el) return "none";
	if (el.tagName === "SELECT") return "select";
	if (el.tagName === "INPUT") {
		const t = (el.type || "").toLowerCase();
		if (t === "checkbox" || t === "radio") return t;
		if (["date", "time", "datetime-local", "month", "week", "color", "range"].includes(t)) return "picker";
	}
	const role = el.getAttribute("role");
	if (role === "checkbox" || role === "switch" || role === "radio") return "checkbox";
	return "text";
})(document.querySelector(%s))`

const selectOptionJS = `((el, v) => {
	if (!el) return false;
	const o = [...el.options].find(o => o.value === v || o.text.trim() === v);
	if (!o) return false;
	el.value = o.value;
	el.dispatchEvent(new Event("input", {bubbles: true}));
	el.dispatchEvent(new Event("change", {bubbles: true}));
	return true;
})(document.querySelector(%s), %s)`

const fillPickerJS = `((el, v) => {
	if (!el) return {ok: false, err: "element not found"};
	try {
		el.focus();
		el.value = v;
		el.dispatchEvent(new Event("input", {bubbles: true}));
		el.dispatchEvent(new Event("change", {bubbles: true}));
		if (v !== "" && (!el.value || el.value === "")) {
			return {ok: false, err: "value " + JSON.stringify(v) + " did not stick to input[type=" + el.type + "]"};
		}
		return {ok: true, val: el.value};
	} catch (e) {
		return {ok: false, err: e.message};
	}
})(document.querySelector(%s), %s)`

const checkedJS = `(el => el ? (el.checked === true || el.getAttribute("aria-checked") === "true") : false)(document.querySelector(%s))`

func fillOne(ctx context.Context, sess *tabSession, el, value string, timeout time.Duration) error {
	sel, err := sess.selector(ctx, el)
	if err != nil {
		return err
	}

	q, _ := json.Marshal(sel)
	var kind string
	if err := sess.eval(ctx, fmt.Sprintf(fieldKindJS, q), &kind); err != nil {
		return exit.New(exit.Upstream, "fill %s: %v", el, err)
	}
	if kind == "none" {
		return exit.New(exit.User, "no element matches %q", el)
	}

	switch kind {
	case "picker":
		v, _ := json.Marshal(value)
		var res struct {
			OK  bool   `json:"ok"`
			Val string `json:"val"`
			Err string `json:"err"`
		}
		if err := sess.eval(ctx, fmt.Sprintf(fillPickerJS, q, v), &res); err != nil {
			return exit.New(exit.Upstream, "fill picker %s: %v", el, err)
		}
		if !res.OK {
			return exit.New(exit.User, "failed to fill %s: %s", el, res.Err)
		}
		return nil

	case "select":
		v, _ := json.Marshal(value)
		var ok bool
		if err := sess.eval(ctx, fmt.Sprintf(selectOptionJS, q, v), &ok); err != nil {
			return exit.New(exit.Upstream, "fill select %s: %v", el, err)
		}
		if !ok {
			return exit.New(exit.User, "%s has no option with value or text %q", el, value)
		}
		return nil

	case "checkbox", "radio":
		want := value == "true"
		if !want && value != "false" {
			return exit.New(exit.Validation, "%s is a %s: value must be 'true' or 'false'", el, kind)
		}
		var on bool
		_ = sess.eval(ctx, fmt.Sprintf(checkedJS, q), &on)
		if on == want {
			return nil
		}
		// Click to toggle
		x, y, err := center(ctx, sess, el, timeout)
		if err != nil {
			return err
		}
		_, _ = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMousePressed,
			Button:     input.MouseButtonLeft,
			Buttons:    1,
			ClickCount: 1,
			X:          x,
			Y:          y,
		})
		_, _ = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMouseReleased,
			Button:     input.MouseButtonLeft,
			Buttons:    0,
			ClickCount: 1,
			X:          x,
			Y:          y,
		})
		return nil
	}

	// Text input or textarea or contenteditable: focus, select all, insert
	focusScript := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return false;
		el.focus();
		if (typeof el.select === "function") {
			el.select();
		} else {
			document.execCommand("selectAll");
		}
		return true;
	})()`, q)

	var focused bool
	if err := sess.eval(ctx, focusScript, &focused); err != nil || !focused {
		// Fallback to clicking it first
		x, y, err := center(ctx, sess, el, timeout)
		if err != nil {
			return err
		}
		_, _ = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMousePressed,
			Button:     input.MouseButtonLeft,
			Buttons:    1,
			ClickCount: 1,
			X:          x,
			Y:          y,
		})
		_, _ = cdp.Call(ctx, sess.conn, input.DispatchMouseEvent, input.DispatchMouseEventParams{
			Type:       input.DispatchMouseEventTypeMouseReleased,
			Button:     input.MouseButtonLeft,
			Buttons:    0,
			ClickCount: 1,
			X:          x,
			Y:          y,
		})
		_ = sess.eval(ctx, focusScript, nil)
	}

	if value == "" {
		_ = sess.eval(ctx, `document.execCommand("delete")`, nil)
		return nil
	}

	_, err = cdp.Call(ctx, sess.conn, input.InsertText, input.InsertTextParams{Text: value})
	if err != nil {
		return exit.New(exit.Upstream, "insert text: %v", err)
	}
	return nil
}

func (d *Daemon) typeText(tabID, text, submit string, timeout time.Duration) (ActResult, error) {
	var submitCombo keyCombo
	if submit != "" {
		c, err := parseCombo(submit)
		if err != nil {
			return ActResult{}, err
		}
		submitCombo = c
	}

	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		if text != "" {
			_, err := cdp.Call(ctx, sess.conn, input.InsertText, input.InsertTextParams{Text: text})
			if err != nil {
				return exit.New(exit.Upstream, "type text: %v", err)
			}
		}

		if submit != "" {
			_, err := cdp.Call(ctx, sess.conn, input.DispatchKeyEvent, input.DispatchKeyEventParams{
				Type:                  input.DispatchKeyEventTypeRawKeyDown,
				Key:                   submitCombo.key,
				Code:                  submitCombo.code,
				WindowsVirtualKeyCode: submitCombo.vk,
				Modifiers:             submitCombo.modifiers,
				Commands:              submitCombo.commands,
			})
			if err != nil {
				return err
			}
			_, _ = cdp.Call(ctx, sess.conn, input.DispatchKeyEvent, input.DispatchKeyEventParams{
				Type:                  input.DispatchKeyEventTypeKeyUp,
				Key:                   submitCombo.key,
				Code:                  submitCombo.code,
				WindowsVirtualKeyCode: submitCombo.vk,
				Modifiers:             submitCombo.modifiers,
			})
		}
		return nil
	})
}

func (d *Daemon) key(tabID, keys string, timeout time.Duration) (ActResult, error) {
	combo, err := parseCombo(keys)
	if err != nil {
		return ActResult{}, err
	}

	return d.actOn(tabID, timeout, func(ctx context.Context, sess *tabSession) error {
		keyType := input.DispatchKeyEventTypeRawKeyDown
		if combo.text != "" && combo.modifiers == 0 {
			keyType = input.DispatchKeyEventTypeKeyDown
		}

		_, err := cdp.Call(ctx, sess.conn, input.DispatchKeyEvent, input.DispatchKeyEventParams{
			Type:                  keyType,
			Key:                   combo.key,
			Code:                  combo.code,
			Text:                  combo.text,
			UnmodifiedText:        combo.text,
			WindowsVirtualKeyCode: combo.vk,
			Modifiers:             combo.modifiers,
			Commands:              combo.commands,
		})
		if err != nil {
			return exit.New(exit.Upstream, "key down: %v", err)
		}

		if combo.text != "" && combo.modifiers == 0 {
			_, _ = cdp.Call(ctx, sess.conn, input.DispatchKeyEvent, input.DispatchKeyEventParams{
				Type:                  input.DispatchKeyEventTypeChar,
				Key:                   combo.key,
				Code:                  combo.code,
				Text:                  combo.text,
				UnmodifiedText:        combo.text,
				WindowsVirtualKeyCode: combo.vk,
			})
		}

		_, err = cdp.Call(ctx, sess.conn, input.DispatchKeyEvent, input.DispatchKeyEventParams{
			Type:                  input.DispatchKeyEventTypeKeyUp,
			Key:                   combo.key,
			Code:                  combo.code,
			WindowsVirtualKeyCode: combo.vk,
			Modifiers:             combo.modifiers,
		})
		if err != nil {
			return exit.New(exit.Upstream, "key up: %v", err)
		}
		return nil
	})
}

// Screenshot captures the page visible viewport as PNG bytes.
func (d *Daemon) screenshot(tabID string, timeout time.Duration) ([]byte, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	buf, err := cdp.Call(ctx, sess.conn, page.CaptureScreenshot, page.CaptureScreenshotParams{
		Format: page.CaptureScreenshotFormatPng,
	})
	if err != nil {
		return nil, exit.New(exit.Upstream, "capture screenshot: %v", err)
	}
	return buf.Data, nil
}

// Read extracts visible innerText from the page or a scoped ref/selector, budgeted by max chars.
func (d *Daemon) read(tabID, ref string, max int, timeout time.Duration) (string, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return "", err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if err := sess.errIfDialog(); err != nil {
		return "", err
	}

	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var js string
	if ref == "" {
		js = `(document.querySelector('main, article') || document.body || document.documentElement).innerText || ""`
	} else {
		sel, err := sess.selector(ctx, ref)
		if err != nil {
			sel = ref
		}
		js = fmt.Sprintf(`(function(){ var el = document.querySelector(%q); return el ? (el.innerText || "") : ""; })()`, sel)
	}

	var text string
	if err := sess.eval(ctx, js, &text); err != nil {
		return "", err
	}

	if max <= 0 {
		max = 20000
	}

	runes := []rune(text)
	if len(runes) > max {
		truncatedCount := len(runes) - max
		text = string(runes[:max]) + fmt.Sprintf("\n…[truncated %d chars]", truncatedCount)
	}

	return text, nil
}
