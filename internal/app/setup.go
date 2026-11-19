package app

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"bxw/internal/browser"
	"bxw/internal/daemon"
	"bxw/internal/exit"
)

func runSetup(args []string) error {
	revert := false
	var browserName string

	for _, a := range args {
		switch a {
		case "--revert":
			revert = true
		default:
			if !strings.HasPrefix(a, "-") && browserName == "" {
				browserName = a
			}
		}
	}

	name, err := resolveBrowser(browserName)
	if err != nil {
		return err
	}
	b, ok := browser.Find(name)
	if !ok {
		return exit.New(exit.NotFound, "browser %q not found", name)
	}

	if revert {
		if err := b.SetDebugging(false); err != nil {
			return err
		}
		fmt.Printf("reverted: remote debugging disabled for %s\n", b.Label)
		return nil
	}

	if b.Running() {
		return exit.New(exit.User, "quit %s first: it rewrites its settings on exit", b.Label)
	}

	if err := b.SetDebugging(true); err != nil {
		return err
	}

	fmt.Printf("✓ enabled remote debugging for %s in Local State\n", b.Label)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Launch %s normally\n", b.Label)
	fmt.Printf("  2. When an agent first attaches via bxw, %s will ask to 'Allow remote debugging'\n", b.Label)
	fmt.Printf("  3. Click 'Allow' — the bxw background daemon maintains the connection so you only click once\n")
	fmt.Printf("  (To revert at any time: bxw setup %s --revert)\n", b.Name)
	return nil
}

type doctorCheck struct {
	Name   string
	OK     bool
	Detail string
}

func runDoctor() error {
	out, err := inspectDoctor("")
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func inspectDoctor(browserFilter string) (string, error) {
	var buf strings.Builder
	buf.WriteString("bxw doctor — " + Version + "\n")
	bs := browser.Installed()
	if len(bs) == 0 {
		return "", exit.New(exit.NotFound, "no supported Chromium browser found on this machine")
	}

	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")

	for _, b := range bs {
		if browserFilter != "" && b.Name != browserFilter {
			continue
		}
		// 1. Installed
		fmt.Fprintf(w, "ok\t%s installed\t%s\n", b.Label, b.Exe)

		// 2. Profiles
		ps, err := browser.Profiles(b)
		if err != nil {
			fmt.Fprintf(w, "WARN\t%s profiles\tunreadable: %v\n", b.Label, err)
		} else {
			fmt.Fprintf(w, "ok\t%s profiles\t%d profile(s)\n", b.Label, len(ps))
		}

		// 3. Running
		running := "not running"
		if b.Running() {
			running = "running"
		}
		fmt.Fprintf(w, "info\t%s process\t%s\n", b.Label, running)

		// 4. Remote debugging pref
		on, err := b.DebuggingEnabled()
		if err != nil || !on {
			fmt.Fprintf(w, "info\t%s real debugging\toff (enable with: bxw setup %s or use: bxw launch %s)\n", b.Label, b.Name, b.Name)
		} else {
			fmt.Fprintf(w, "ok\t%s real debugging\ton (Local State pref set)\n", b.Label)
		}

		// 5. Active CDP connection / DevToolsActivePort
		ws, err := b.WSURL()
		if err == nil {
			fmt.Fprintf(w, "ok\t%s CDP reachability\tactive (%s)\n", b.Label, ws)
		} else {
			fmt.Fprintf(w, "info\t%s CDP reachability\tinactive\n", b.Label)
		}

		// 6. Daemon
		daemonStatus := "stopped"
		if daemon.Running(b.Name) {
			daemonStatus = "running (connected)"
		}
		fmt.Fprintf(w, "info\t%s daemon\t%s\n", b.Label, daemonStatus)
	}

	if err := w.Flush(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
