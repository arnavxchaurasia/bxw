package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"

	"bxw/internal/cache"
	"bxw/internal/exit"
)

type ringBuffer struct {
	mu    sync.Mutex
	items []map[string]any
	cap   int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 500
	}
	return &ringBuffer{cap: capacity}
}

func (r *ringBuffer) add(m map[string]any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, m)
	if len(r.items) > r.cap {
		r.items = r.items[len(r.items)-r.cap:]
	}
}

func (r *ringBuffer) list() []map[string]any {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.items))
	copy(out, r.items)
	return out
}

func (sess *tabSession) startDebugCapture(ctx context.Context) {
	if sess.consoleBuf == nil {
		sess.consoleBuf = newRingBuffer(500)
	}
	if sess.netBuf == nil {
		sess.netBuf = newRingBuffer(500)
	}

	// 1. Console API calls
	consoleCh := cdp.Events(ctx, sess.conn, runtime.ConsoleAPICalled)
	go func() {
		for ev, err := range consoleCh {
			if err != nil {
				return
			}
			var parts []string
			for _, arg := range ev.Args {
				if arg != nil {
					if len(arg.Value) > 0 {
						parts = append(parts, string(arg.Value))
					} else if arg.Description != "" {
						parts = append(parts, arg.Description)
					}
				}
			}
			url := ""
			line := int64(0)
			if ev.StackTrace != nil && len(ev.StackTrace.CallFrames) > 0 {
				url = ev.StackTrace.CallFrames[0].URL
				line = ev.StackTrace.CallFrames[0].LineNumber
			}
			sess.consoleBuf.add(map[string]any{
				"t":     time.Now().UTC().Format(time.RFC3339Nano),
				"level": string(ev.Type),
				"text":  strings.Join(parts, " "),
				"url":   url,
				"line":  line,
			})
		}
	}()

	// 2. Runtime exceptions
	exCh := cdp.Events(ctx, sess.conn, runtime.ExceptionThrown)
	go func() {
		for ev, err := range exCh {
			if err != nil {
				return
			}
			msg := ev.ExceptionDetails.Text
			if ev.ExceptionDetails.Exception != nil && ev.ExceptionDetails.Exception.Description != "" {
				msg = ev.ExceptionDetails.Exception.Description
			}
			sess.consoleBuf.add(map[string]any{
				"t":     time.Now().UTC().Format(time.RFC3339Nano),
				"level": "error",
				"text":  msg,
				"url":   ev.ExceptionDetails.URL,
				"line":  ev.ExceptionDetails.LineNumber,
			})
		}
	}()

	// 3. Log entries
	logCh := cdp.Events(ctx, sess.conn, log.EntryAdded)
	go func() {
		for ev, err := range logCh {
			if err != nil {
				return
			}
			if ev.Entry != nil {
				sess.consoleBuf.add(map[string]any{
					"t":     time.Now().UTC().Format(time.RFC3339Nano),
					"level": string(ev.Entry.Level),
					"text":  ev.Entry.Text,
					"url":   ev.Entry.URL,
					"line":  ev.Entry.LineNumber,
				})
			}
		}
	}()

	// 4. Network tracking
	pendingNetMu := sync.Mutex{}
	pendingNet := make(map[network.RequestID]map[string]any)

	reqCh := cdp.Events(ctx, sess.conn, network.RequestWillBeSent)
	respCh := cdp.Events(ctx, sess.conn, network.ResponseReceived)
	finishCh := cdp.Events(ctx, sess.conn, network.LoadingFinished)
	failCh := cdp.Events(ctx, sess.conn, network.LoadingFailed)

	go func() {
		for ev, err := range reqCh {
			if err != nil {
				return
			}
			sess.netMu.Lock()
			if sess.inFlightReqs == nil {
				sess.inFlightReqs = make(map[network.RequestID]time.Time)
			}
			sess.inFlightReqs[ev.RequestID] = time.Now()
			sess.netMu.Unlock()

			pendingNetMu.Lock()
			rec := map[string]any{
				"id":         string(ev.RequestID),
				"method":     ev.Request.Method,
				"url":        ev.Request.URL,
				"type":       string(ev.Type),
				"start":      time.Now(),
				"reqHeaders": ev.Request.Headers,
			}
			pendingNet[ev.RequestID] = rec
			pendingNetMu.Unlock()
		}
	}()

	go func() {
		for ev, err := range respCh {
			if err != nil {
				return
			}
			pendingNetMu.Lock()
			rec, ok := pendingNet[ev.RequestID]
			if !ok {
				rec = map[string]any{
					"id":    string(ev.RequestID),
					"type":  string(ev.Type),
					"start": time.Now(),
				}
				pendingNet[ev.RequestID] = rec
			}
			if ev.Response != nil {
				rec["url"] = ev.Response.URL
				rec["status"] = ev.Response.Status
				rec["respHeaders"] = ev.Response.Headers
				rec["mimeType"] = ev.Response.MimeType
			}
			pendingNetMu.Unlock()
		}
	}()

	go func() {
		for ev, err := range finishCh {
			if err != nil {
				return
			}
			sess.netMu.Lock()
			delete(sess.inFlightReqs, ev.RequestID)
			sess.netMu.Unlock()

			pendingNetMu.Lock()
			rec, ok := pendingNet[ev.RequestID]
			if ok {
				delete(pendingNet, ev.RequestID)
				if startTime, ok := rec["start"].(time.Time); ok {
					rec["ms"] = time.Since(startTime).Milliseconds()
					delete(rec, "start")
				}
				sess.netBuf.add(rec)
			}
			pendingNetMu.Unlock()
		}
	}()

	go func() {
		for ev, err := range failCh {
			if err != nil {
				return
			}
			sess.netMu.Lock()
			delete(sess.inFlightReqs, ev.RequestID)
			sess.netMu.Unlock()

			pendingNetMu.Lock()
			rec, ok := pendingNet[ev.RequestID]
			if ok {
				delete(pendingNet, ev.RequestID)
				rec["error"] = ev.ErrorText
				if startTime, ok := rec["start"].(time.Time); ok {
					rec["ms"] = time.Since(startTime).Milliseconds()
					delete(rec, "start")
				}
				sess.netBuf.add(rec)
			}
			pendingNetMu.Unlock()
		}
	}()
}

// inFlightCount returns the current number of in-flight network requests.
func (sess *tabSession) inFlightCount() int {
	if sess == nil {
		return 0
	}
	sess.netMu.Lock()
	defer sess.netMu.Unlock()
	return len(sess.inFlightReqs)
}

func (d *Daemon) console(tabID, level string, limit int) ([]map[string]any, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return nil, err
	}

	items := sess.consoleBuf.list()
	var filtered []map[string]any
	levelLower := strings.ToLower(strings.TrimSpace(level))

	for _, item := range items {
		if levelLower != "" {
			itemLvl, _ := item["level"].(string)
			if !strings.EqualFold(itemLvl, levelLower) {
				continue
			}
		}
		filtered = append(filtered, item)
	}

	if limit <= 0 {
		limit = 50
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}

func (d *Daemon) network(tabID, filter, requestID string, limit int) (any, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return nil, err
	}

	if requestID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		sess.mu.Lock()
		bodyBytes, err := cdp.Call(ctx, sess.conn, network.GetResponseBody, network.GetResponseBodyParams{
			RequestID: network.RequestID(requestID),
		})
		sess.mu.Unlock()

		if err != nil {
			return nil, exit.New(exit.Upstream, "get response body: %v", err)
		}

		bodyStr := string(bodyBytes.Body)
		if len(bodyBytes.Body) > 64*1024 {
			uri, _, err := cache.Put(bodyBytes.Body, ".txt")
			if err == nil {
				bodyStr = uri
			}
		}

		// Find matching record from buffer
		items := sess.netBuf.list()
		var matched map[string]any
		for _, it := range items {
			if it["id"] == requestID {
				matched = it
				break
			}
		}
		return map[string]any{
			"id":       requestID,
			"metadata": matched,
			"body":     bodyStr,
		}, nil
	}

	items := sess.netBuf.list()
	var filtered []map[string]any
	filterLower := strings.ToLower(strings.TrimSpace(filter))

	for _, item := range items {
		if filterLower != "" {
			url, _ := item["url"].(string)
			if !strings.Contains(strings.ToLower(url), filterLower) {
				continue
			}
		}
		filtered = append(filtered, item)
	}

	if limit <= 0 {
		limit = 50
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}

func (d *Daemon) storage(tabID string, timeout time.Duration) (map[string]any, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cookies, err := cdp.Call(ctx, sess.conn, network.GetCookies, network.GetCookiesParams{})
	if err != nil {
		return nil, exit.New(exit.Upstream, "get cookies: %v", err)
	}

	var webStorage struct {
		Local   map[string]any `json:"local"`
		Session map[string]any `json:"session"`
	}
	storageScript := `JSON.stringify({local: {...localStorage}, session: {...sessionStorage}})`
	var storageRaw string
	_ = sess.eval(ctx, storageScript, &storageRaw)
	if storageRaw != "" {
		_ = json.Unmarshal([]byte(storageRaw), &webStorage)
	}

	return map[string]any{
		"cookies": cookies.Cookies,
		"local":   webStorage.Local,
		"session": webStorage.Session,
	}, nil
}

func (d *Daemon) performance(tabID string, timeout time.Duration) (map[string]any, error) {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	const perfScript = `(() => {
		const nav = performance.getEntriesByType('navigation')[0] || {};
		const ttfb = nav.responseStart || 0;

		const paints = performance.getEntriesByType('paint');
		const fcpEntry = paints.find(p => p.name === 'first-contentful-paint');
		const fcp = fcpEntry ? fcpEntry.startTime : 0;

		const lcps = performance.getEntriesByType('largest-contentful-paint');
		const lcp = lcps.length > 0 ? lcps[lcps.length - 1].startTime : 0;

		const shifts = performance.getEntriesByType('layout-shift') || [];
		let cls = 0;
		for (const s of shifts) {
			if (!s.hadRecentInput) {
				cls += s.value;
			}
		}

		const longtasks = (performance.getEntriesByType('longtask') || []).map(lt => ({
			name: lt.name,
			duration: lt.duration,
			startTime: lt.startTime,
		}));

		const resources = (performance.getEntriesByType('resource') || [])
			.map(r => ({ name: r.name, duration: r.duration, type: r.initiatorType, size: r.transferSize || 0 }))
			.sort((a, b) => b.duration - a.duration)
			.slice(0, 5);

		const domSize = document.getElementsByTagName('*').length;
		const usedHeap = performance.memory ? performance.memory.usedJSHeapSize : 0;

		// Ratings per web.dev Core Web Vitals guidance:
		// LCP: good <= 2500ms, poor > 4000ms
		// FCP: good <= 1800ms, poor > 3000ms
		// CLS: good <= 0.1, poor > 0.25
		// TTFB: good <= 800ms, poor > 1800ms
		const rate = (val, good, poor) => val <= good ? "good" : (val <= poor ? "needs-improvement" : "poor");

		return {
			ttfb: { value: ttfb, rating: rate(ttfb, 800, 1800) },
			fcp: { value: fcp, rating: rate(fcp, 1800, 3000) },
			lcp: { value: lcp, rating: rate(lcp, 2500, 4000) },
			cls: { value: cls, rating: rate(cls, 0.1, 0.25) },
			longtasks: longtasks,
			slowest_resources: resources,
			dom_size: domSize,
			used_heap_bytes: usedHeap,
		};
	})()`

	var res map[string]any
	if err := sess.eval(ctx, perfScript, &res); err != nil {
		return nil, err
	}
	return res, nil
}

type emulateOpts struct {
	Device  string
	Network string
	CPU     float64
	Geo     string
	Media   string
}

func (d *Daemon) emulate(tabID string, opts emulateOpts, timeout time.Duration) error {
	sess, err := d.tabSession(tabID)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 1. Device metrics
	switch strings.ToLower(opts.Device) {
	case "mobile":
		if _, err := cdp.Call(ctx, sess.conn, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width:             390,
			Height:            844,
			DeviceScaleFactor: 3,
			Mobile:            true,
		}); err != nil {
			return exit.New(exit.Upstream, "set mobile emulation: %v", err)
		}
	case "desktop", "reset":
		if _, err := cdp.Call(ctx, sess.conn, emulation.ClearDeviceMetricsOverride, cdp.Empty{}); err != nil {
			return exit.New(exit.Upstream, "clear device emulation: %v", err)
		}
	}

	// 2. Network conditions
	switch strings.ToLower(opts.Network) {
	case "offline":
		if _, err := cdp.Call(ctx, sess.conn, network.EmulateNetworkConditionsByRule, network.EmulateNetworkConditionsByRuleParams{
			MatchedNetworkConditions: []*network.Conditions{
				{Offline: true},
			},
		}); err != nil {
			return exit.New(exit.Upstream, "set offline: %v", err)
		}
	case "slow3g":
		if _, err := cdp.Call(ctx, sess.conn, network.EmulateNetworkConditionsByRule, network.EmulateNetworkConditionsByRuleParams{
			MatchedNetworkConditions: []*network.Conditions{
				{
					Offline:            false,
					Latency:            400,
					DownloadThroughput: 400 * 1024 / 8,
					UploadThroughput:   400 * 1024 / 8,
				},
			},
		}); err != nil {
			return exit.New(exit.Upstream, "set slow3g: %v", err)
		}
	case "online", "reset":
		if _, err := cdp.Call(ctx, sess.conn, network.EmulateNetworkConditionsByRule, network.EmulateNetworkConditionsByRuleParams{
			MatchedNetworkConditions: []*network.Conditions{
				{
					Offline:            false,
					Latency:            0,
					DownloadThroughput: -1,
					UploadThroughput:   -1,
				},
			},
		}); err != nil {
			return exit.New(exit.Upstream, "reset network emulation: %v", err)
		}
	}

	// 3. CPU throttling
	if opts.CPU > 0 {
		rate := opts.CPU
		if opts.CPU == 1 {
			rate = 1
		}
		if _, err := cdp.Call(ctx, sess.conn, emulation.SetCPUThrottlingRate, emulation.SetCPUThrottlingRateParams{
			Rate: rate,
		}); err != nil {
			return exit.New(exit.Upstream, "set cpu throttling: %v", err)
		}
	}

	// 4. Geolocation
	if opts.Geo != "" {
		if strings.ToLower(opts.Geo) == "reset" {
			if _, err := cdp.Call(ctx, sess.conn, emulation.ClearGeolocationOverride, cdp.Empty{}); err != nil {
				return exit.New(exit.Upstream, "clear geolocation: %v", err)
			}
		} else {
			var lat, lng float64
			n, _ := fmt.Sscanf(opts.Geo, "%f,%f", &lat, &lng)
			if n == 2 {
				acc := 1.0
				if _, err := cdp.Call(ctx, sess.conn, emulation.SetGeolocationOverride, emulation.SetGeolocationOverrideParams{
					Latitude:  &lat,
					Longitude: &lng,
					Accuracy:  &acc,
				}); err != nil {
					return exit.New(exit.Upstream, "set geolocation: %v", err)
				}
			}
		}
	}

	// 5. Emulated media
	switch strings.ToLower(opts.Media) {
	case "dark":
		if _, err := cdp.Call(ctx, sess.conn, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: "dark"},
			},
		}); err != nil {
			return exit.New(exit.Upstream, "set dark mode: %v", err)
		}
	case "light":
		if _, err := cdp.Call(ctx, sess.conn, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: "light"},
			},
		}); err != nil {
			return exit.New(exit.Upstream, "set light mode: %v", err)
		}
	case "reset":
		if _, err := cdp.Call(ctx, sess.conn, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{},
		}); err != nil {
			return exit.New(exit.Upstream, "reset media emulation: %v", err)
		}
	}

	return nil
}
