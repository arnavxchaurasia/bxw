// Package app is the CLI/MCP surface. M0 ships discovery; M2/M3 add the daemon,
// agent launch, and the first live tab enumeration over CDP; M4 adds DOM/AX snapshot
// and ref-based actions (click, fill, type, key, eval).
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"bxw/internal/browser"
	"bxw/internal/cache"
	"bxw/internal/contextmgr"
	"bxw/internal/daemon"
	"bxw/internal/exit"
	"bxw/internal/history"
	"bxw/internal/osport"
	"bxw/internal/perms"
)

// Version is overridden at build time via -ldflags "-X bxw/internal/app.Version=...".
var Version = "0.0.0-dev"

// Run dispatches a command line (os.Args[1:]).
func Run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println("bxw", Version)
		return nil
	case "list":
		return runList(args[1:])
	case "launch":
		return runLaunch(args[1:])
	case "login":
		return runLogin(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "query":
		return runQuery(args[1:])
	case "screenshot":
		return runScreenshot(args[1:])
	case "click":
		return runClick(args[1:])
	case "hover":
		return runHover(args[1:])
	case "drag":
		return runDrag(args[1:])
	case "upload":
		return runUpload(args[1:])
	case "fill":
		return runFill(args[1:])
	case "type":
		return runType(args[1:])
	case "key":
		return runKey(args[1:])
	case "eval":
		return runEval(args[1:])
	case "cdp":
		return runCDP(args[1:])
	case "open":
		return runOpen(args[1:])
	case "close-tab", "close_tab":
		return runCloseTab(args[1:])
	case "goto":
		return runGoto(args[1:])
	case "wait":
		return runWait(args[1:])
	case "read":
		return runRead(args[1:])
	case "dialog":
		return runDialog(args[1:])
	case "console":
		return runConsole(args[1:])
	case "network":
		return runNetwork(args[1:])
	case "storage":
		return runStorage(args[1:])
	case "performance":
		return runPerformance(args[1:])
	case "emulate":
		return runEmulate(args[1:])
	case "perms":
		return runPerms(args[1:])
	case "history":
		return runHistory(args[1:])
	case "context":
		return runContext(args[1:])
	case "clean":
		return runClean(args[1:])
	case "setup":
		return runSetup(args[1:])
	case "mcp":
		return runMCP(args[1:])
	case "stop":
		return runStop(args[1:])
	case "doctor":
		return runDoctor()
	case "__daemon": // internal: the background singleton entrypoint
		return runDaemon(args[1:])
	case "help", "-h", "--help":
		return usage()
	default:
		return exit.New(exit.Usage, "unknown command %q (try: bxw help)", args[0])
	}
}

func usage() error {
	fmt.Print(`bxw — drive your Windows Chromium browser by profile over CDP

usage:
  bxw list browsers                   list installed Chromium browsers
  bxw list profiles <browser>         list a browser's profiles
  bxw launch <browser> [url]          start an isolated agent instance (CDP on)
  bxw login <browser> [url]           open persistent profile headed to log in once
  bxw list tabs [browser]             list open tabs of the bxw-managed instance
  bxw open <url> [--profile <name>] [--follow]   open url in named profile window
  bxw close-tab <tab>                 close one tab
  bxw goto <tab> <url> [--follow]     navigate the current tab (vs open = new tab)
  bxw wait <tab> [--selector s|--text t|--load|--ms N]   wait for a condition
  bxw read <tab> [--ref e12] [--max 20000]   read the page's visible text
  bxw dialog <tab> <accept|dismiss> [--text t] respond to a JavaScript dialog
  bxw console <tab> [--level lvl] [--limit N]  tail page console and error logs
  bxw network <tab> [--filter s] [--id reqID]  inspect captured network requests and responses
  bxw storage <tab>                           inspect cookies, localStorage and sessionStorage
  bxw performance <tab>                       audit Core Web Vitals (LCP, FCP, CLS, TTFB, memory)
  bxw emulate <tab> [flags]                   emulate mobile/desktop, network, cpu, geo, dark mode
                                              flags: --device, --network, --cpu, --geo, --media
  bxw setup <browser> [--revert]      enable/disable remote debugging on real profile
  bxw mcp [install [client]]          speak MCP over stdio or install into AI clients
  bxw snapshot <tab> [flags]          accessibility snapshot with stable element refs (e1, e2...)
                                      flags: --full, --ref <ref>, --find <text>, --budget <N>, --out <file>
  bxw query <tab> [flags]             filter and rank elements by role, name, text, proximity
                                      flags: --role <r>, --name <n>, --text <t>, --near <label>, --limit <k>, --json
  bxw screenshot <tab> [flags]        capture viewport PNG, saving to cache (prints bxw://cache link)
                                      flags: --out <file>
  bxw click <tab> <ref|sel> [--double] [--verbose] click an element by ref or CSS selector
  bxw hover <tab> <ref|sel> [--verbose] hover over an element by ref or CSS selector
  bxw drag <tab> <from> <to> [--verbose] drag element to destination element
  bxw upload <tab> <ref|sel> <file>.. [--verbose] set files on an input[type=file] element
  bxw fill <tab> <ref|sel> <val>... [--verbose] fill form inputs, selects, or toggles
  bxw type <tab> <text> [--submit <k>] [--verbose] type text into active element
  bxw key <tab> <combo> [--verbose]   press key or combination (e.g. Enter, Control+A)
  bxw eval <tab> <expr>               evaluate JavaScript on the tab
  bxw cdp <tab> <Domain.method> [--params <json>]   send any DevTools command
  bxw perms [list|grant|revoke]       inspect and manage capability permissions
  bxw history [browser] [flags]       query navigation history (--since, --grep, --profile)
  bxw context [browser] [--session id] view session context summary
  bxw clean [--all]                   clean cache, stale state, and logs
  bxw stop <browser>                  stop the daemon
  bxw doctor                          self-check this machine
  bxw version
`)
	return nil
}

func runList(args []string) error {
	if len(args) == 0 {
		return exit.New(exit.Usage, "usage: bxw list <browsers|profiles|tabs> [browser]")
	}
	switch args[0] {
	case "browsers":
		bs := browser.Installed()
		if len(bs) == 0 {
			return exit.New(exit.NotFound, "no supported Chromium browser found")
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tEXE\tUSER DATA")
		for _, b := range bs {
			fmt.Fprintf(w, "%s\t%s\t%s\n", b.Name, b.Exe, b.DataDir)
		}
		return w.Flush()
	case "profiles":
		if len(args) < 2 {
			return exit.New(exit.Usage, "usage: bxw list profiles <browser>")
		}
		b, ok := browser.Find(args[1])
		if !ok {
			return exit.New(exit.NotFound, "browser %q not installed (try: bxw list browsers)", args[1])
		}
		ps, err := browser.Profiles(b)
		if err != nil {
			return exit.New(exit.Upstream, "%v", err)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "DIR\tNAME")
		for _, p := range ps {
			fmt.Fprintf(w, "%s\t%s\n", p.Dir, p.Name)
		}
		return w.Flush()
	case "tabs":
		var profileName, browserName string
		for i := 0; i < len(args[1:]); i++ {
			switch args[1:][i] {
			case "--profile":
				if i+1 < len(args[1:]) {
					profileName = args[1:][i+1]
					i++
				}
			case "--browser":
				if i+1 < len(args[1:]) {
					browserName = args[1:][i+1]
					i++
				}
			default:
				if strings.HasPrefix(args[1:][i], "--profile=") {
					profileName = strings.TrimPrefix(args[1:][i], "--profile=")
				} else if strings.HasPrefix(args[1:][i], "--browser=") {
					browserName = strings.TrimPrefix(args[1:][i], "--browser=")
				} else if !strings.HasPrefix(args[1:][i], "-") && browserName == "" {
					browserName = args[1:][i]
				}
			}
		}

		name, err := resolveBrowser(browserName)
		if err != nil {
			return err
		}
		data, err := daemon.Call(name, "list_tabs", map[string]any{"profile": profileName})
		if err != nil {
			return exit.New(exit.Upstream, "%v", err)
		}
		var tabs []daemon.Tab
		if err := json.Unmarshal(data, &tabs); err != nil {
			return exit.New(exit.Internal, "decode tabs: %v", err)
		}
		if len(tabs) == 0 {
			fmt.Println("(no open tabs)")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		hasProfiles := false
		for _, t := range tabs {
			if t.Profile != "" || t.ProfileDir != "" {
				hasProfiles = true
				break
			}
		}

		if hasProfiles {
			fmt.Fprintln(w, "ID\tPROFILE\tTITLE\tURL")
			for _, t := range tabs {
				prof := t.Profile
				if prof == "" {
					prof = t.ProfileDir
				}
				if prof == "" {
					prof = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", short(t.ID), prof, t.Title, t.URL)
			}
		} else {
			fmt.Fprintln(w, "ID\tTITLE\tURL")
			for _, t := range tabs {
				fmt.Fprintf(w, "%s\t%s\t%s\n", short(t.ID), t.Title, t.URL)
			}
		}
		return w.Flush()
	default:
		return exit.New(exit.Usage, "unknown list target %q (want: browsers|profiles|tabs)", args[0])
	}
}

func runLaunch(args []string) error {
	if len(args) < 1 {
		return exit.New(exit.Usage, "usage: bxw launch <browser> [url] [--headless] [--real-profile]")
	}
	headless := false
	realProfile := false
	targetURL := "about:blank"
	var browserName string

	for _, a := range args {
		if a == "--headless" {
			headless = true
		} else if a == "--real-profile" {
			realProfile = true
		} else if browserName == "" && !strings.HasPrefix(a, "-") {
			browserName = a
		} else if targetURL == "about:blank" && !strings.HasPrefix(a, "-") {
			targetURL = a
		}
	}

	b, ok := browser.Find(browserName)
	if !ok {
		return exit.New(exit.NotFound, "browser %q not installed (try: bxw list browsers)", browserName)
	}

	if realProfile && b.Name != "brave" {
		return exit.New(exit.User, "real-profile remote debugging is blocked on %s by Chromium 136; persistent profile is used instead", b.Label)
	}

	data, err := daemon.Call(b.Name, "launch", map[string]any{
		"headless":     headless,
		"url":          targetURL,
		"real_profile": realProfile,
	})
	if err != nil {
		return exit.New(exit.Upstream, "%v", err)
	}
	var res map[string]any
	_ = json.Unmarshal(data, &res)
	ws := res["ws"]
	if ws == nil {
		ws = string(data)
	}
	fmt.Printf("launched %s agent instance (CDP ready: %v)\n", b.Label, ws)
	return nil
}

func runLogin(args []string) error {
	if len(args) < 1 {
		return exit.New(exit.Usage, "usage: bxw login <browser> [url]")
	}
	targetURL := "https://google.com"
	var browserName string
	for _, a := range args {
		if browserName == "" && !strings.HasPrefix(a, "-") {
			browserName = a
		} else if !strings.HasPrefix(a, "-") {
			targetURL = a
		}
	}

	b, ok := browser.Find(browserName)
	if !ok {
		return exit.New(exit.NotFound, "browser %q not installed (try: bxw list browsers)", browserName)
	}

	fmt.Printf("Launching %s with persistent profile for login at %s...\n", b.Label, targetURL)
	data, err := daemon.Call(b.Name, "launch", map[string]any{
		"headless":     false,
		"url":          targetURL,
		"real_profile": false,
	})
	if err != nil {
		return exit.New(exit.Upstream, "%v", err)
	}
	var res map[string]any
	_ = json.Unmarshal(data, &res)
	fmt.Printf("Persistent profile opened. Log into your accounts in the opened browser window.\nSession tokens will persist across future bxw runs.\n")
	return nil
}

func runSnapshot(args []string) error {
	var pos []string
	var browserName, outFile, scopeRef, findQuery string
	full := false
	budget := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--full":
			full = true
		case "--budget":
			if i+1 < len(args) {
				budget, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--ref":
			if i+1 < len(args) {
				scopeRef = args[i+1]
				i++
			}
		case "--find":
			if i+1 < len(args) {
				findQuery = args[i+1]
				i++
			}
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--out":
			if i+1 < len(args) {
				outFile = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--out=") {
				outFile = strings.TrimPrefix(args[i], "--out=")
			} else if strings.HasPrefix(args[i], "--ref=") {
				scopeRef = strings.TrimPrefix(args[i], "--ref=")
			} else if strings.HasPrefix(args[i], "--find=") {
				findQuery = strings.TrimPrefix(args[i], "--find=")
			} else if strings.HasPrefix(args[i], "--budget=") {
				budget, _ = strconv.Atoi(strings.TrimPrefix(args[i], "--budget="))
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	tab := ""
	if len(pos) >= 1 {
		tab = pos[0]
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	callArgs := map[string]any{
		"tab":  tab,
		"full": full,
		"ref":  scopeRef,
		"find": findQuery,
	}
	if budget > 0 {
		callArgs["budget"] = budget
	}

	data, err := daemon.Call(name, "snapshot", callArgs)
	if err != nil {
		return err
	}

	var snap daemon.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return exit.New(exit.Internal, "decode snapshot: %v", err)
	}

	text := snap.Text()
	if outFile != "" {
		if err := os.WriteFile(outFile, []byte(text), 0o644); err != nil {
			return exit.New(exit.User, "write %s: %v", outFile, err)
		}
		fmt.Printf("wrote %s (%d elements)\n", outFile, len(snap.Nodes))
		return nil
	}

	fmt.Print(text)
	return nil
}

func runQuery(args []string) error {
	var pos []string
	var browserName, role, name, text, near string
	var limit int
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--role":
			if i+1 < len(args) {
				role = args[i+1]
				i++
			}
		case "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "--text":
			if i+1 < len(args) {
				text = args[i+1]
				i++
			}
		case "--near":
			if i+1 < len(args) {
				near = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		case "--json":
			jsonOutput = true
		default:
			if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	tab := ""
	if len(pos) >= 1 {
		tab = pos[0]
	}

	bName, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	callArgs := map[string]any{
		"tab": tab,
	}
	if role != "" {
		callArgs["role"] = role
	}
	if name != "" {
		callArgs["name"] = name
	}
	if text != "" {
		callArgs["text"] = text
	}
	if near != "" {
		callArgs["near"] = near
	}
	if limit > 0 {
		callArgs["limit"] = limit
	}

	data, err := daemon.Call(bName, "query", callArgs)
	if err != nil {
		return err
	}

	var res daemon.QueryResult
	if err := json.Unmarshal(data, &res); err != nil {
		return exit.New(exit.Internal, "decode query result: %v", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	fmt.Print(res.Text())
	return nil
}

func runScreenshot(args []string) error {
	var pos []string
	var browserName, outFile string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--out":
			if i+1 < len(args) {
				outFile = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--out=") {
				outFile = strings.TrimPrefix(args[i], "--out=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	tab := ""
	if len(pos) >= 1 {
		tab = pos[0]
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "screenshot", map[string]any{"tab": tab})
	if err != nil {
		return err
	}

	uri, _, err := cache.Put(data, ".png")
	if err != nil {
		return exit.New(exit.Internal, "cache screenshot: %v", err)
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, data, 0o644); err != nil {
			return exit.New(exit.User, "write %s: %v", outFile, err)
		}
		fmt.Printf("wrote %s (%s)\n", outFile, uri)
		return nil
	}

	fmt.Println(uri)
	return nil
}

func runClick(args []string) error {
	var pos []string
	var browserName string
	double := false
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--double":
			double = true
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw click <tab> <element> [--double] [--verbose]")
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "click", map[string]any{
		"tab":      pos[0],
		"selector": pos[1],
		"double":   double,
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	_ = json.Unmarshal(data, &res)

	action := "clicked"
	if double {
		action = "double-clicked"
	}
	fmt.Printf("%s %s\n", action, pos[1])
	if res.Delta != "" {
		fmt.Printf("delta: %s\n", res.Delta)
	} else if res.Navigated {
		fmt.Printf("navigated to %s (%s)\n", res.URL, res.Title)
	}
	if verbose && res.RawDiff != "" {
		fmt.Println("--- raw diff ---")
		fmt.Print(res.RawDiff)
	}
	return nil
}

func runHover(args []string) error {
	var pos []string
	var browserName string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw hover <tab> <ref|sel> [--verbose] [--browser <name>]")
	}

	tab := pos[0]
	el := pos[1]

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "hover", map[string]any{
		"tab":      tab,
		"selector": el,
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	if err := json.Unmarshal(data, &res); err == nil {
		fmt.Println(res.TerseSummary("hover " + el))
		if verbose && res.RawDiff != "" {
			fmt.Println("--- raw diff ---")
			fmt.Print(res.RawDiff)
		}
		return nil
	}
	fmt.Printf("hover %s -> done\n", el)
	return nil
}

func runDrag(args []string) error {
	var pos []string
	var browserName string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 3 {
		return exit.New(exit.Usage, "usage: bxw drag <tab> <from> <to> [--verbose] [--browser <name>]")
	}

	tab := pos[0]
	from := pos[1]
	to := pos[2]

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "drag", map[string]any{
		"tab":  tab,
		"from": from,
		"to":   to,
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	if err := json.Unmarshal(data, &res); err == nil {
		fmt.Println(res.TerseSummary(fmt.Sprintf("drag %s -> %s", from, to)))
		if verbose && res.RawDiff != "" {
			fmt.Println("--- raw diff ---")
			fmt.Print(res.RawDiff)
		}
		return nil
	}
	fmt.Printf("drag %s -> %s -> done\n", from, to)
	return nil
}

func runUpload(args []string) error {
	var pos []string
	var browserName string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 3 {
		return exit.New(exit.Usage, "usage: bxw upload <tab> <ref|sel> <file>... [--verbose] [--browser <name>]")
	}

	tab := pos[0]
	el := pos[1]
	files := pos[2:]

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "upload", map[string]any{
		"tab":      tab,
		"selector": el,
		"paths":    files,
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	if err := json.Unmarshal(data, &res); err == nil {
		fmt.Println(res.TerseSummary(fmt.Sprintf("uploaded %d file(s) to %s", len(files), el)))
		if verbose && res.RawDiff != "" {
			fmt.Println("--- raw diff ---")
			fmt.Print(res.RawDiff)
		}
		return nil
	}
	fmt.Printf("uploaded %d file(s) to %s\n", len(files), el)
	return nil
}

func runFill(args []string) error {
	var pos []string
	var browserName string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 3 || len(pos)%2 != 1 {
		return exit.New(exit.Usage, "usage: bxw fill <tab> <element> <value> [<element> <value> ...] [--verbose]")
	}

	tab := pos[0]
	var pairs [][2]string
	for i := 1; i < len(pos); i += 2 {
		pairs = append(pairs, [2]string{pos[i], pos[i+1]})
	}
	pairsJSON, _ := json.Marshal(pairs)

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "fill", map[string]any{
		"tab":   tab,
		"pairs": string(pairsJSON),
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	_ = json.Unmarshal(data, &res)

	fmt.Printf("filled %d field(s)\n", len(pairs))
	if res.Delta != "" {
		fmt.Printf("delta: %s\n", res.Delta)
	} else if res.Navigated {
		fmt.Printf("navigated to %s (%s)\n", res.URL, res.Title)
	}
	if verbose && res.RawDiff != "" {
		fmt.Println("--- raw diff ---")
		fmt.Print(res.RawDiff)
	}
	return nil
}

func runType(args []string) error {
	var pos []string
	var browserName, submit string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--submit":
			if i+1 < len(args) {
				submit = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--submit=") {
				submit = strings.TrimPrefix(args[i], "--submit=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw type <tab> <text> [--submit <key>] [--verbose]")
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "type", map[string]any{
		"tab":    pos[0],
		"text":   pos[1],
		"submit": submit,
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	_ = json.Unmarshal(data, &res)

	fmt.Printf("typed %q\n", pos[1])
	if res.Delta != "" {
		fmt.Printf("delta: %s\n", res.Delta)
	} else if res.Navigated {
		fmt.Printf("navigated to %s (%s)\n", res.URL, res.Title)
	}
	if verbose && res.RawDiff != "" {
		fmt.Println("--- raw diff ---")
		fmt.Print(res.RawDiff)
	}
	return nil
}

func runKey(args []string) error {
	var pos []string
	var browserName string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose":
			verbose = true
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw key <tab> <key> [--verbose] (such as Enter, Tab, Control+A)")
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "key", map[string]any{
		"tab":  pos[0],
		"keys": pos[1],
	})
	if err != nil {
		return err
	}

	var res daemon.ActResult
	_ = json.Unmarshal(data, &res)

	fmt.Printf("pressed %s\n", pos[1])
	if res.Delta != "" {
		fmt.Printf("delta: %s\n", res.Delta)
	} else if res.Navigated {
		fmt.Printf("navigated to %s (%s)\n", res.URL, res.Title)
	}
	if verbose && res.RawDiff != "" {
		fmt.Println("--- raw diff ---")
		fmt.Print(res.RawDiff)
	}
	return nil
}

func runEval(args []string) error {
	var pos []string
	var browserName, filePath string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--file":
			if i+1 < len(args) {
				filePath = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--file=") {
				filePath = strings.TrimPrefix(args[i], "--file=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	var script string
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return exit.New(exit.User, "read %s: %v", filePath, err)
		}
		script = string(b)
	} else if len(pos) >= 2 {
		script = pos[1]
	} else {
		return exit.New(exit.Usage, "usage: bxw eval <tab> '<expression>' | bxw eval <tab> --file <script.js>")
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw eval <tab> '<expression>'")
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "eval", map[string]any{
		"tab":    pos[0],
		"script": script,
	})
	if err != nil {
		return err
	}

	var formatted any
	if err := json.Unmarshal(data, &formatted); err == nil {
		pretty, _ := json.MarshalIndent(formatted, "", "  ")
		fmt.Println(string(pretty))
	} else {
		fmt.Println(string(data))
	}
	return nil
}

func runCDP(args []string) error {
	var pos []string
	var browserName, paramsStr string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--params":
			if i+1 < len(args) {
				paramsStr = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--params=") {
				paramsStr = strings.TrimPrefix(args[i], "--params=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw cdp <tab> <Domain.method> [--params <json>]")
	}

	tab := pos[0]
	method := pos[1]

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "cdp", map[string]any{
		"tab":    tab,
		"method": method,
		"params": paramsStr,
	})
	if err != nil {
		return err
	}

	var formatted any
	if err := json.Unmarshal(data, &formatted); err == nil {
		pretty, _ := json.MarshalIndent(formatted, "", "  ")
		fmt.Println(string(pretty))
	} else {
		fmt.Println(string(data))
	}
	return nil
}

func runOpen(args []string) error {
	var pos []string
	var browserName, profileName string
	follow := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--profile":
			if i+1 < len(args) {
				profileName = args[i+1]
				i++
			}
		case "--follow":
			follow = true
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--profile=") {
				profileName = strings.TrimPrefix(args[i], "--profile=")
			} else if args[i] == "--follow=true" {
				follow = true
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw open <url> [--profile <name>] [--follow] [--browser <name>]")
	}

	rawURL := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	callArgs := map[string]any{
		"url":     rawURL,
		"profile": profileName,
	}
	if follow {
		callArgs["follow"] = true
	}

	data, err := daemon.Call(name, "open", callArgs)
	if err != nil {
		return err
	}

	var tab daemon.Tab
	_ = json.Unmarshal(data, &tab)

	profStr := ""
	if tab.Profile != "" {
		profStr = fmt.Sprintf(" in %s (%s)", tab.Profile, tab.ProfileDir)
	}
	fmt.Printf("opened %s%s: %s (%s)\n", short(tab.ID), profStr, tab.Title, tab.URL)
	return nil
}

func runCloseTab(args []string) error {
	var pos []string
	var browserName string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw close-tab <tab> [--browser <name>]")
	}

	tab := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	_, err = daemon.Call(name, "close_tab", map[string]any{
		"tab": tab,
	})
	if err != nil {
		return err
	}

	fmt.Printf("closed tab %s\n", short(tab))
	return nil
}

func runGoto(args []string) error {
	var pos []string
	var browserName string
	follow := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--follow":
			follow = true
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if args[i] == "--follow=true" {
				follow = true
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw goto <tab> <url> [--follow] [--browser <name>]")
	}

	tab := pos[0]
	targetURL := pos[1]

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	callArgs := map[string]any{
		"tab": tab,
		"url": targetURL,
	}
	if follow {
		callArgs["follow"] = true
	}

	data, err := daemon.Call(name, "goto", callArgs)
	if err != nil {
		return err
	}

	var res daemon.ActResult
	if err := json.Unmarshal(data, &res); err == nil {
		fmt.Println(res.TerseSummary("goto"))
		return nil
	}
	fmt.Println(string(data))
	return nil
}

func runWait(args []string) error {
	var pos []string
	var browserName string
	var selector string
	var text string
	var load bool
	var ms int
	var timeoutStr string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--selector":
			if i+1 < len(args) {
				selector = args[i+1]
				i++
			}
		case "--text":
			if i+1 < len(args) {
				text = args[i+1]
				i++
			}
		case "--load":
			load = true
		case "--ms":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &ms)
				i++
			}
		case "--timeout":
			if i+1 < len(args) {
				timeoutStr = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--selector=") {
				selector = strings.TrimPrefix(args[i], "--selector=")
			} else if strings.HasPrefix(args[i], "--text=") {
				text = strings.TrimPrefix(args[i], "--text=")
			} else if strings.HasPrefix(args[i], "--ms=") {
				fmt.Sscanf(strings.TrimPrefix(args[i], "--ms="), "%d", &ms)
			} else if strings.HasPrefix(args[i], "--timeout=") {
				timeoutStr = strings.TrimPrefix(args[i], "--timeout=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw wait <tab> [--selector s|--text t|--load|--ms N] [--timeout <dur>] [--browser <name>]")
	}

	tab := pos[0]

	count := 0
	if selector != "" {
		count++
	}
	if text != "" {
		count++
	}
	if load {
		count++
	}
	if ms > 0 {
		count++
	}
	if count != 1 {
		return exit.New(exit.Usage, "exactly one of --selector, --text, --load, or --ms must be specified")
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	var timeoutMs int
	if timeoutStr != "" {
		if dur, err := time.ParseDuration(timeoutStr); err == nil {
			timeoutMs = int(dur.Milliseconds())
		}
	}

	waitArgs := map[string]any{
		"tab": tab,
	}
	if selector != "" {
		waitArgs["selector"] = selector
	}
	if text != "" {
		waitArgs["text"] = text
	}
	if load {
		waitArgs["load"] = true
	}
	if ms > 0 {
		waitArgs["ms"] = ms
	}
	if timeoutMs > 0 {
		waitArgs["timeout_ms"] = timeoutMs
	}

	data, err := daemon.Call(name, "wait", waitArgs)
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	return nil
}

func runRead(args []string) error {
	var pos []string
	var browserName string
	var ref string
	var max int

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--ref":
			if i+1 < len(args) {
				ref = args[i+1]
				i++
			}
		case "--max":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &max)
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--ref=") {
				ref = strings.TrimPrefix(args[i], "--ref=")
			} else if strings.HasPrefix(args[i], "--max=") {
				fmt.Sscanf(strings.TrimPrefix(args[i], "--max="), "%d", &max)
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw read <tab> [--ref e12] [--max 20000] [--browser <name>]")
	}

	tab := pos[0]

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	readArgs := map[string]any{
		"tab": tab,
	}
	if ref != "" {
		readArgs["ref"] = ref
	}
	if max > 0 {
		readArgs["max"] = max
	}

	data, err := daemon.Call(name, "read", readArgs)
	if err != nil {
		return err
	}

	var res struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &res); err == nil {
		fmt.Println(res.Text)
		return nil
	}

	fmt.Println(string(data))
	return nil
}

func runDialog(args []string) error {
	var pos []string
	var browserName string
	var promptText string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--text":
			if i+1 < len(args) {
				promptText = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--text=") {
				promptText = strings.TrimPrefix(args[i], "--text=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 2 {
		return exit.New(exit.Usage, "usage: bxw dialog <tab> <accept|dismiss> [--text <prompt>] [--browser <name>]")
	}

	tab := pos[0]
	action := pos[1]
	if action != "accept" && action != "dismiss" {
		return exit.New(exit.Validation, "dialog action must be 'accept' or 'dismiss'")
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	dialogArgs := map[string]any{
		"tab":    tab,
		"action": action,
	}
	if promptText != "" {
		dialogArgs["prompt_text"] = promptText
	}

	data, err := daemon.Call(name, "dialog", dialogArgs)
	if err != nil {
		return err
	}

	fmt.Printf("dialog %s -> handled\n", action)
	_ = data
	return nil
}

func runConsole(args []string) error {
	var pos []string
	var browserName, level string
	var limit int

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--level":
			if i+1 < len(args) {
				level = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--level=") {
				level = strings.TrimPrefix(args[i], "--level=")
			} else if strings.HasPrefix(args[i], "--limit=") {
				fmt.Sscanf(strings.TrimPrefix(args[i], "--limit="), "%d", &limit)
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw console <tab> [--level <info|warning|error>] [--limit <n>] [--browser <name>]")
	}

	tab := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	cArgs := map[string]any{"tab": tab}
	if level != "" {
		cArgs["level"] = level
	}
	if limit > 0 {
		cArgs["limit"] = limit
	}

	data, err := daemon.Call(name, "console", cArgs)
	if err != nil {
		return err
	}

	var logs []map[string]any
	if err := json.Unmarshal(data, &logs); err == nil {
		if len(logs) == 0 {
			fmt.Println("(no console messages)")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tLEVEL\tMESSAGE\tLOCATION")
		for _, l := range logs {
			tStr, _ := l["t"].(string)
			if len(tStr) > 19 {
				tStr = tStr[11:19]
			}
			loc := ""
			if url, ok := l["url"].(string); ok && url != "" {
				line, _ := l["line"].(float64)
				loc = fmt.Sprintf("%s:%d", filepath.Base(url), int(line))
			}
			fmt.Fprintf(w, "%s\t%v\t%v\t%s\n", tStr, l["level"], l["text"], loc)
		}
		return w.Flush()
	}

	fmt.Println(string(data))
	return nil
}

func runNetwork(args []string) error {
	var pos []string
	var browserName, filter, reqID string
	var limit int

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--filter":
			if i+1 < len(args) {
				filter = args[i+1]
				i++
			}
		case "--id":
			if i+1 < len(args) {
				reqID = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--filter=") {
				filter = strings.TrimPrefix(args[i], "--filter=")
			} else if strings.HasPrefix(args[i], "--id=") {
				reqID = strings.TrimPrefix(args[i], "--id=")
			} else if strings.HasPrefix(args[i], "--limit=") {
				fmt.Sscanf(strings.TrimPrefix(args[i], "--limit="), "%d", &limit)
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw network <tab> [--filter <substr>] [--id <reqID>] [--limit <n>] [--browser <name>]")
	}

	tab := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	nArgs := map[string]any{"tab": tab}
	if filter != "" {
		nArgs["filter"] = filter
	}
	if reqID != "" {
		nArgs["request_id"] = reqID
	}
	if limit > 0 {
		nArgs["limit"] = limit
	}

	data, err := daemon.Call(name, "network", nArgs)
	if err != nil {
		return err
	}

	if reqID != "" {
		var detail map[string]any
		if err := json.Unmarshal(data, &detail); err == nil {
			pretty, _ := json.MarshalIndent(detail, "", "  ")
			fmt.Println(string(pretty))
			return nil
		}
		fmt.Println(string(data))
		return nil
	}

	var reqs []map[string]any
	if err := json.Unmarshal(data, &reqs); err == nil {
		if len(reqs) == 0 {
			fmt.Println("(no network requests captured)")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tMETHOD\tSTATUS\tTYPE\tDURATION\tURL")
		for _, r := range reqs {
			dur := "-"
			if ms, ok := r["ms"].(float64); ok {
				dur = fmt.Sprintf("%.0fms", ms)
			}
			status := "-"
			if st, ok := r["status"].(float64); ok && st > 0 {
				status = fmt.Sprintf("%.0f", st)
			}
			urlStr, _ := r["url"].(string)
			if len(urlStr) > 60 {
				urlStr = urlStr[:57] + "..."
			}
			fmt.Fprintf(w, "%v\t%v\t%s\t%v\t%s\t%s\n", r["id"], r["method"], status, r["type"], dur, urlStr)
		}
		return w.Flush()
	}

	fmt.Println(string(data))
	return nil
}

func runStorage(args []string) error {
	var pos []string
	var browserName string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw storage <tab> [--browser <name>]")
	}

	tab := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "storage", map[string]any{"tab": tab})
	if err != nil {
		return err
	}

	var res map[string]any
	if err := json.Unmarshal(data, &res); err == nil {
		pretty, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(pretty))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

func runPerformance(args []string) error {
	var pos []string
	var browserName string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw performance <tab> [--browser <name>]")
	}

	tab := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	data, err := daemon.Call(name, "performance", map[string]any{"tab": tab})
	if err != nil {
		return err
	}

	var res map[string]any
	if err := json.Unmarshal(data, &res); err == nil {
		pretty, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(pretty))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

func runEmulate(args []string) error {
	var pos []string
	var browserName, device, netCond, geo, media string
	var cpu float64

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
			if i+1 < len(args) {
				browserName = args[i+1]
				i++
			}
		case "--device":
			if i+1 < len(args) {
				device = args[i+1]
				i++
			}
		case "--network":
			if i+1 < len(args) {
				netCond = args[i+1]
				i++
			}
		case "--cpu":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%f", &cpu)
				i++
			}
		case "--geo":
			if i+1 < len(args) {
				geo = args[i+1]
				i++
			}
		case "--media":
			if i+1 < len(args) {
				media = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--browser=") {
				browserName = strings.TrimPrefix(args[i], "--browser=")
			} else if strings.HasPrefix(args[i], "--device=") {
				device = strings.TrimPrefix(args[i], "--device=")
			} else if strings.HasPrefix(args[i], "--network=") {
				netCond = strings.TrimPrefix(args[i], "--network=")
			} else if strings.HasPrefix(args[i], "--cpu=") {
				fmt.Sscanf(strings.TrimPrefix(args[i], "--cpu="), "%f", &cpu)
			} else if strings.HasPrefix(args[i], "--geo=") {
				geo = strings.TrimPrefix(args[i], "--geo=")
			} else if strings.HasPrefix(args[i], "--media=") {
				media = strings.TrimPrefix(args[i], "--media=")
			} else if !strings.HasPrefix(args[i], "-") {
				pos = append(pos, args[i])
			}
		}
	}

	if len(pos) < 1 {
		return exit.New(exit.Usage, "usage: bxw emulate <tab> [--device <mobile|desktop|reset>] [--network <offline|slow3g|online|reset>] [--cpu <N>] [--geo <lat,lng|reset>] [--media <dark|light|reset>]")
	}

	tab := pos[0]
	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	callArgs := map[string]any{
		"tab": tab,
	}
	if device != "" {
		callArgs["device"] = device
	}
	if netCond != "" {
		callArgs["network"] = netCond
	}
	if cpu > 0 {
		callArgs["cpu"] = cpu
	}
	if geo != "" {
		callArgs["geo"] = geo
	}
	if media != "" {
		callArgs["media"] = media
	}

	if _, err := daemon.Call(name, "emulate", callArgs); err != nil {
		return err
	}

	fmt.Println("Emulation settings applied successfully.")
	return nil
}

func runPerms(args []string) error {
	if len(args) == 0 || args[0] == "list" {
		records := perms.Default().List()
		if len(records) == 0 {
			fmt.Println("(no permissions granted)")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ORIGIN\tCAPABILITY\tSTATE\tSCOPE\tEXPIRES")
		for _, r := range records {
			exp := "never"
			if !r.ExpiresAt.IsZero() {
				exp = r.ExpiresAt.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Origin, r.Capability, r.State, r.Scope, exp)
		}
		return w.Flush()
	}

	switch args[0] {
	case "grant":
		if len(args) < 3 {
			return exit.New(exit.Usage, "usage: bxw perms grant <origin> <capability> [--always]")
		}
		origin := args[1]
		capName := args[2]
		scope := perms.ScopeSession
		for _, a := range args[3:] {
			if a == "--always" {
				scope = perms.ScopeAlways
			}
		}
		if err := perms.Default().Grant(origin, capName, scope, 24*time.Hour); err != nil {
			return exit.New(exit.Internal, "grant permission: %v", err)
		}
		fmt.Printf("granted %s for %s (scope: %s)\n", capName, origin, scope)
		return nil

	case "revoke":
		if len(args) < 2 {
			return exit.New(exit.Usage, "usage: bxw perms revoke <origin> [capability]")
		}
		origin := args[1]
		capName := "*"
		if len(args) >= 3 {
			capName = args[2]
		}
		if err := perms.Default().Revoke(origin, capName); err != nil {
			return exit.New(exit.Internal, "revoke permission: %v", err)
		}
		fmt.Printf("revoked %s for %s\n", capName, origin)
		return nil

	default:
		return exit.New(exit.Usage, "usage: bxw perms [list|grant|revoke]")
	}
}

func runHistory(args []string) error {
	var browserName string
	var sinceStr, grepStr, profileStr string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--since":
			if i+1 < len(args) {
				sinceStr = args[i+1]
				i++
			}
		case "--grep":
			if i+1 < len(args) {
				grepStr = args[i+1]
				i++
			}
		case "--profile":
			if i+1 < len(args) {
				profileStr = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--since=") {
				sinceStr = strings.TrimPrefix(args[i], "--since=")
			} else if strings.HasPrefix(args[i], "--grep=") {
				grepStr = strings.TrimPrefix(args[i], "--grep=")
			} else if strings.HasPrefix(args[i], "--profile=") {
				profileStr = strings.TrimPrefix(args[i], "--profile=")
			} else if !strings.HasPrefix(args[i], "-") && browserName == "" {
				browserName = args[i]
			}
		}
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}

	filter := history.Filter{
		Grep:    grepStr,
		Profile: profileStr,
	}
	if sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			filter.Since = t
		} else if d, err := time.ParseDuration(sinceStr); err == nil {
			filter.Since = time.Now().Add(-d)
		}
	}

	entries, err := history.Query(name, filter)
	if err != nil {
		return exit.New(exit.Internal, "query history: %v", err)
	}
	if len(entries) == 0 {
		fmt.Println("(no history recorded)")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tPROFILE\tURL\tTITLE")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Timestamp.Format("2006-01-02 15:04:05"), e.Profile, e.URL, e.Title)
	}
	return w.Flush()
}

func runContext(args []string) error {
	var sessionID string
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			sessionID = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--session=") {
			sessionID = strings.TrimPrefix(args[i], "--session=")
		}
	}

	sc, err := contextmgr.LoadSession(sessionID)
	if err != nil {
		return exit.New(exit.Internal, "load context: %v", err)
	}
	fmt.Print(sc.Summary())
	return nil
}

func runClean(args []string) error {
	all := false
	for _, a := range args {
		if a == "--all" {
			all = true
		}
	}

	count, bytes, err := cache.Clean()
	if err != nil {
		return exit.New(exit.Internal, "clean cache: %v", err)
	}
	fmt.Printf("cleaned cache: removed %d blobs (%.2f MB)\n", count, float64(bytes)/(1024*1024))

	p := osport.Current()
	dataRoot := p.DataRoot()

	// Clean stale top-level legacy files/directories in DataRoot
	if entries, err := os.ReadDir(dataRoot); err == nil {
		for _, e := range entries {
			name := e.Name()
			fullPath := filepath.Join(dataRoot, name)
			// Remove orphaned agent dirs (brave-agent, edge-agent, test-agent-*, etc.)
			if e.IsDir() {
				if strings.HasSuffix(name, "-agent") || strings.HasPrefix(name, "test-") {
					_ = os.RemoveAll(fullPath)
				}
			} else {
				// Remove legacy top-level .lock, .sock, .log, .port files
				if strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".sock") ||
					strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".port") {
					_ = os.Remove(fullPath)
				}
			}
		}
	}

	// Clean stale files in run/ whose owning daemon is not running
	runDir := p.RunDir()
	if entries, err := os.ReadDir(runDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			fullPath := filepath.Join(runDir, name)
			// e.g. edge.sock, edge.lock, edge.port
			browserName := strings.Split(name, ".")[0]
			if !daemon.Running(browserName) {
				_ = os.Remove(fullPath)
			}
		}
	}

	if all {
		// Clean logs
		logsDir := p.LogsDir()
		if entries, err := os.ReadDir(logsDir); err == nil {
			for _, e := range entries {
				_ = os.Remove(filepath.Join(logsDir, e.Name()))
			}
		}
		// Clean state
		baseStateDir := filepath.Join(p.DataRoot(), "state")
		_ = os.RemoveAll(baseStateDir)
		_ = os.MkdirAll(baseStateDir, 0o755)
		fmt.Println("cleaned logs and transient tab states")
	}
	return nil
}

func runStop(args []string) error {
	if len(args) < 1 {
		return exit.New(exit.Usage, "usage: bxw stop <browser>")
	}
	if _, err := daemon.Call(args[0], "stop", nil); err != nil {
		return exit.New(exit.Upstream, "%v", err)
	}
	fmt.Printf("stopped daemon for %s\n", args[0])
	return nil
}

func runDaemon(args []string) error {
	name := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--browser" && i+1 < len(args) {
			name = args[i+1]
			i++
		}
	}
	if name == "" {
		return exit.New(exit.Usage, "__daemon requires --browser <name>")
	}
	return daemon.Run(name)
}

func resolveBrowser(flag string) (string, error) {
	if flag != "" {
		if _, ok := browser.Find(flag); !ok {
			return "", exit.New(exit.NotFound, "browser %q not found (try: bxw list browsers)", flag)
		}
		return flag, nil
	}
	bs := browser.Installed()
	for _, b := range bs {
		if daemon.Running(b.Name) {
			return b.Name, nil
		}
	}
	if len(bs) > 0 {
		return bs[0].Name, nil
	}
	return "", exit.New(exit.NotFound, "no supported Chromium browser found")
}

func browserFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--browser" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], "--browser=") {
			return strings.TrimPrefix(args[i], "--browser=")
		}
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
