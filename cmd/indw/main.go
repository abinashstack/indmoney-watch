package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/alert"
	"github.com/abinashstack/indmoney-watch/internal/config"
	"github.com/abinashstack/indmoney-watch/internal/indmoney"
	"github.com/abinashstack/indmoney-watch/internal/mcpclient"
	"github.com/abinashstack/indmoney-watch/internal/notify"
	"github.com/abinashstack/indmoney-watch/internal/oauth"
	"github.com/abinashstack/indmoney-watch/internal/state"
	"github.com/abinashstack/indmoney-watch/internal/store"
)

const (
	mcpEndpoint   = "https://mcp.indmoney.com/mcp"
	launchdLabel  = "indmoney-watch"
)

func usage() {
	fmt.Fprintln(os.Stderr, `indw — INDmoney portfolio + watchlist alerts

Usage:
  indw login [-f]                      OAuth (no-op if already logged in; -f forces re-auth)
  indw status                          Show current snapshot
  indw watchlist                       Show watchlist with live prices
  indw sips                            Show stock + MF SIPs (amount, frequency, next, status)
  indw set-target SYMBOL above PRICE   Add price target (above)
  indw set-target SYMBOL below PRICE   Add price target (below)
  indw clear-target SYMBOL             Remove targets for a symbol
  indw run-once                        Single poll (alerts fire if thresholds hit)
  indw start                           Install launchd agent (poll every 10 min, 09:00–16:00 IST)
  indw stop                            Uninstall launchd agent
  indw config                          Print config path and contents
  indw logs [-f]                       Show launchd agent log (-f to follow)
  indw state                           Show last snapshot + debounce state
  indw paths                           Show all on-disk locations
  indw menubar                         Print SwiftBar menu plugin output (renders portfolio in macOS menu bar)
  indw menubar install                 Install as a SwiftBar plugin (prompts to install SwiftBar if missing)`)
	fmt.Fprintln(os.Stderr)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	ctx := context.Background()

	switch cmd {
	case "login":
		must(cmdLogin(ctx))
	case "status":
		must(cmdStatus(ctx))
	case "watchlist":
		must(cmdWatchlist(ctx))
	case "sips":
		must(cmdSIPs(ctx))
	case "set-target":
		must(cmdSetTarget(args))
	case "clear-target":
		must(cmdClearTarget(args))
	case "run-once":
		must(cmdRunOnce(ctx))
	case "start":
		must(cmdStart())
	case "stop":
		must(cmdStop())
	case "config":
		must(cmdConfig())
	case "logs":
		must(cmdLogs(args))
	case "state":
		must(cmdState())
	case "paths":
		must(cmdPaths())
	case "menubar":
		must(cmdMenubar(ctx, args))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// ---- commands ----

func cmdLogin(ctx context.Context) error {
	// Short-circuit if a usable session already exists. We treat a session as
	// usable if either the access token is still valid OR a refresh succeeds.
	force := false
	for _, a := range os.Args[2:] {
		if a == "-f" || a == "--force" {
			force = true
		}
	}
	if !force {
		if t, err := store.LoadTokens(); err == nil {
			if time.Until(t.ExpiresAt) > 60*time.Second {
				fmt.Println("Already logged in. Token valid until", t.ExpiresAt.Local().Format("2006-01-02 15:04 MST"))
				fmt.Println("Use `indw login --force` to re-authenticate.")
				return nil
			}
			if t.RefreshToken != "" {
				if nt, rerr := oauth.Refresh(ctx, t); rerr == nil {
					if serr := store.SaveTokens(nt); serr == nil {
						fmt.Println("Refreshed existing session. Token valid until", nt.ExpiresAt.Local().Format("2006-01-02 15:04 MST"))
						return nil
					}
				}
			}
			fmt.Println("Existing tokens expired and could not be refreshed; starting full login.")
		}
	}

	// Use a fixed local port so the redirect URI we register is identical to
	// the one we receive on. The IndMoney authorization server enforces strict
	// equality between registered and presented redirect_uri.
	const redirectURI = "http://127.0.0.1:47823/callback"
	creds, err := oauth.Register(ctx, redirectURI)
	if err != nil {
		return fmt.Errorf("dynamic registration: %w", err)
	}
	tokens, _, err := oauth.Login(ctx, creds, redirectURI)
	if err != nil {
		return err
	}
	if err := store.SaveTokens(tokens); err != nil {
		return err
	}
	fmt.Println("Logged in. Tokens stored in macOS Keychain (service: indmoney-watch).")
	return nil
}

func newAPI(ctx context.Context) (*indmoney.API, error) {
	ts, err := store.NewTokenSource()
	if err != nil {
		return nil, fmt.Errorf("load tokens: %w (run `indw login`)", err)
	}
	c := mcpclient.New(mcpEndpoint, ts)
	if err := c.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return indmoney.New(c), nil
}

func cmdStatus(ctx context.Context) error {
	api, err := newAPI(ctx)
	if err != nil {
		return err
	}
	snap, err := api.NetworthSnapshot(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Total invested:\t₹%.2f\n", snap.TotalInvested)
	fmt.Fprintf(tw, "Total current:\t₹%.2f\n", snap.TotalCurrentValue)
	pct := 0.0
	if snap.TotalInvested > 0 {
		pct = (snap.TotalCurrentValue - snap.TotalInvested) / snap.TotalInvested * 100
	}
	fmt.Fprintf(tw, "Total return:\t%+.2f%%\n", pct)
	fmt.Fprintf(tw, "Net worth (assets-liab):\t₹%.2f\n", snap.TotalNetworth)
	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "By asset type:")
	for _, inv := range snap.Investments {
		fmt.Fprintf(tw, "  %s\t₹%.0f\t%+.2f%%\n", inv.AssetType, inv.CurrentValue, inv.ReturnPercentage)
	}
	if len(snap.Liabilities.CreditCards) > 0 {
		fmt.Fprintln(tw)
		fmt.Fprintln(tw, "Credit cards:")
		for _, cc := range snap.Liabilities.CreditCards {
			fmt.Fprintf(tw, "  %s\t₹%.2f due %s\n", cc.Name, cc.TotalDue, cc.DueDate)
		}
	}
	return tw.Flush()
}

func cmdWatchlist(ctx context.Context) error {
	api, err := newAPI(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MARKET\tSYMBOL\tNAME\tLTP\tDAY%\tID")

	// Indian: user_watchlist → get_indian_stocks_details.
	if wl, err := api.Watchlist(ctx, "indian"); err == nil {
		stocks := wl.AllStocks()
		indKeys := make([]string, 0, len(stocks))
		for _, s := range stocks {
			if s.IndKey != "" {
				indKeys = append(indKeys, s.IndKey)
			}
		}
		if len(indKeys) > 0 {
			if details, err := api.IndianStockDetails(ctx, indKeys); err == nil {
				for k, ent := range details {
					fmt.Fprintf(tw, "IND\t%s\t%s\t₹%.2f\t%+.2f\t%s\n",
						ent.Basic.Symbol, trunc(ent.Basic.Name, 30),
						ent.Stats.LivePrice, ent.Stats.DayChangePct, k)
				}
			}
		}
	}

	// US: user_watchlist now returns tickers directly (server fix 2026-05-21).
	if wl, err := api.Watchlist(ctx, "us"); err == nil {
		stocks := wl.AllStocks()
		var tickers []string
		for _, s := range stocks {
			if s.Ticker != "" {
				tickers = append(tickers, s.Ticker)
			}
		}
		if len(tickers) > 0 {
			if details, err := api.USStockDetails(ctx, tickers); err == nil {
				for tkr, ent := range details {
					fmt.Fprintf(tw, "US\t%s\t%s\t$%.2f\t%+.2f\t%s\n",
						ent.Basic.Symbol, trunc(ent.Basic.Name, 30),
						ent.Stats.LivePrice, ent.Stats.DayChangePct, tkr)
				}
			} else {
				fmt.Fprintf(tw, "US\t-\tcouldn't fetch details (%v)\t\t\t\n", err)
			}
		} else {
			fmt.Fprintln(tw, "US\t-\t(empty US watchlist)\t\t\t")
		}
	}

	return tw.Flush()
}

func cmdSIPs(ctx context.Context) error {
	api, err := newAPI(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tAMOUNT\tFREQ\tNEXT\tSTATUS")

	stock, serr := api.IndianStockSIPs(ctx)
	if serr != nil {
		fmt.Fprintf(tw, "STOCK\t-\t-\t-\t-\terror: %v\n", serr)
	} else if len(stock) == 0 {
		fmt.Fprintln(tw, "STOCK\t-\t(no active stock SIPs)\t-\t-\t-")
	} else {
		for _, s := range stock {
			printSIPRow(tw, "STOCK", s)
		}
	}

	mf, merr := api.MFSIPs(ctx)
	if merr != nil {
		fmt.Fprintf(tw, "MF\t-\t-\t-\t-\terror: %v\n", merr)
	} else if len(mf) == 0 {
		fmt.Fprintln(tw, "MF\t-\t(no active MF SIPs)\t-\t-\t-")
	} else {
		for _, s := range mf {
			printSIPRow(tw, "MF", s)
		}
	}

	return tw.Flush()
}

func printSIPRow(tw *tabwriter.Writer, kind string, s indmoney.SIP) {
	status := "-"
	switch s.StatusKind() {
	case indmoney.StatusFailed:
		status = "failed"
	case indmoney.StatusUpcoming:
		status = "upcoming"
	case indmoney.StatusInProgress:
		status = "in_progress"
	case indmoney.StatusExecuted:
		status = "executed"
	}
	freq := s.Frequency
	if freq == "" {
		freq = "-"
	}
	next := s.NextDate()
	if next == "" {
		next = "-"
	}
	fmt.Fprintf(tw, "%s\t%s\t₹%.0f\t%s\t%s\t%s\n",
		kind, trunc(s.DisplayName(), 32), s.AmountValue(), freq, next, status)
}

func cmdSetTarget(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: indw set-target SYMBOL above|below PRICE")
	}
	sym := strings.ToUpper(args[0])
	dir := strings.ToLower(args[1])
	price, err := strconv.ParseFloat(args[2], 64)
	if err != nil {
		return fmt.Errorf("price: %w", err)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	t := cfg.Targets[sym]
	switch dir {
	case "above":
		t.Above = price
	case "below":
		t.Below = price
	default:
		return errors.New(`direction must be "above" or "below"`)
	}
	cfg.Targets[sym] = t
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("Set %s %s ₹%.2f\n", sym, dir, price)
	return nil
}

func cmdClearTarget(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: indw clear-target SYMBOL")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	delete(cfg.Targets, strings.ToUpper(args[0]))
	return config.Save(cfg)
}

func cmdRunOnce(ctx context.Context) error {
	// Cap the entire cycle. The HTTP client has a 30 s per-request timeout but
	// the engine fires ~10–20 sequential MCP calls, so a degraded upstream can
	// otherwise drag a single run past the next launchd slot. 2 minutes is
	// generous for a healthy poll and leaves headroom before the next 5 min
	// fire.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := state.Load()
	if err != nil {
		return err
	}

	api, err := newAPI(ctx)
	if err != nil {
		return notifyIfNeedsLogin(err, st)
	}
	eng := alert.NewEngine(api, cfg, st)
	if err := eng.Run(ctx); err != nil {
		return notifyIfNeedsLogin(err, st)
	}
	return nil
}

// notifyIfNeedsLogin surfaces oauth.ErrNeedsLogin to the user via a macOS
// banner — once per 6h cooldown so the daemon doesn't spam every cycle while
// the refresh token is dead. The original error is returned unchanged so the
// launchd log still records the underlying cause.
func notifyIfNeedsLogin(err error, st *state.State) error {
	if !errors.Is(err, oauth.ErrNeedsLogin) {
		return err
	}
	const key = "auth:needs-login"
	const cooldown = 6 * time.Hour
	now := time.Now()
	if last, ok := st.LastFired[key]; ok && now.Sub(last) < cooldown {
		return err
	}
	st.LastFired[key] = now
	_ = state.Save(st)
	_ = notify.MacBanner(
		"INDmoney session expired",
		"Refresh token rejected",
		"Run `indw login -f` to re-authenticate.",
	)
	return err
}

func cmdConfig() error {
	d, err := config.Dir()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Println("config dir:", d)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(b))
	return nil
}

// ---- launchd ----

// plistEscape XML-escapes a string for safe substitution inside a
// <string>…</string> element of the launchd plist we generate. The two values
// we substitute (`exe` from os.Executable, `logFile` under $HOME/.config) are
// trusted in normal use, but a path containing `<` or `&` would corrupt the
// plist, and a maliciously-crafted path could close the <string> tag and
// inject directives like RunAtLoad=true. Defense in depth: cheaper to escape
// every substitution than to reason about whether each input is safe.
func plistEscape(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func cmdStart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	logDir, err := config.Dir()
	if err != nil {
		return err
	}
	logFile := filepath.Join(logDir, "agent.log")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run-once</string>
  </array>
  <key>StartCalendarInterval</key>
  <array>
%s
  </array>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin</string>
  </dict>
</dict>
</plist>
`, launchdLabel, plistEscape(exe), calendarSlots(), plistEscape(logFile), plistEscape(logFile))

	pp, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(pp, []byte(plist), 0o644); err != nil {
		return err
	}
	// Bootstrap.
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("launchctl", "bootout", target, pp).Run()
	out, err := exec.Command("launchctl", "bootstrap", target, pp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	fmt.Println("Installed launchd agent:", pp)
	fmt.Println("Logs:", logFile)
	fmt.Println("It will run every 5 minutes between 09:00–16:00 IST, Mon–Fri.")
	return nil
}

func cmdStop() error {
	pp, err := plistPath()
	if err != nil {
		return err
	}
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("launchctl", "bootout", target, pp).Run()
	if err := os.Remove(pp); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("Removed launchd agent.")
	return nil
}

// calendarSlots returns StartCalendarInterval entries for every 5 minutes
// between 09:00 and 16:00 IST on Mon-Fri. macOS launchd uses local time,
// so we offset for IST (+05:30) → local. The host's local TZ matters; we
// emit IST minute-of-day slots based on the host's current offset.
func calendarSlots() string {
	// Convert IST hours to host-local hours.
	// 09:00 IST → host local time of 09:00 IST.
	istLoc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		istLoc = time.FixedZone("IST", 5*3600+30*60)
	}
	var sb strings.Builder
	weekdays := []int{1, 2, 3, 4, 5} // Mon-Fri
	// 09:00 to 15:55 IST in 5-min steps (last fire 15:55).
	for h := 9; h <= 15; h++ {
		for m := 0; m < 60; m += 5 {
			istT := time.Date(2026, 1, 5, h, m, 0, 0, istLoc) // any Monday
			localT := istT.Local()
			for _, wd := range weekdays {
				sb.WriteString(fmt.Sprintf(
					"    <dict><key>Weekday</key><integer>%d</integer><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>\n",
					wd, localT.Hour(), localT.Minute(),
				))
			}
		}
	}
	// One last slot at 16:00 IST.
	istT := time.Date(2026, 1, 5, 16, 0, 0, 0, istLoc)
	localT := istT.Local()
	for _, wd := range weekdays {
		sb.WriteString(fmt.Sprintf(
			"    <dict><key>Weekday</key><integer>%d</integer><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>\n",
			wd, localT.Hour(), localT.Minute(),
		))
	}
	return sb.String()
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ---- inspection commands ----

func cmdLogs(args []string) error {
	d, err := config.Dir()
	if err != nil {
		return err
	}
	logFile := filepath.Join(d, "agent.log")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		fmt.Println("(no log yet — agent hasn't run; use `indw start` to install it, or `indw run-once` to test)")
		return nil
	}
	tailArgs := []string{"-n", "200"}
	for _, a := range args {
		if a == "-f" || a == "--follow" {
			tailArgs = append(tailArgs, "-f")
		}
	}
	tailArgs = append(tailArgs, logFile)
	c := exec.Command("tail", tailArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func cmdState() error {
	d, err := config.Dir()
	if err != nil {
		return err
	}
	stateFile := filepath.Join(d, "state.json")
	b, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		fmt.Println("(no state yet — run `indw run-once` to create one)")
		return nil
	}
	if err != nil {
		return err
	}
	// Pretty-print whatever we have.
	var v any
	if json.Unmarshal(b, &v) == nil {
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(string(b))
	return nil
}

func cmdPaths() error {
	d, err := config.Dir()
	if err != nil {
		return err
	}
	exe, _ := os.Executable()
	pp, _ := plistPath()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "binary\t%s\n", exe)
	fmt.Fprintf(tw, "config dir\t%s\n", d)
	fmt.Fprintf(tw, "config\t%s\n", filepath.Join(d, "config.yaml"))
	fmt.Fprintf(tw, "state\t%s\n", filepath.Join(d, "state.json"))
	fmt.Fprintf(tw, "agent log\t%s\n", filepath.Join(d, "agent.log"))
	fmt.Fprintf(tw, "launchd plist\t%s\n", pp)
	fmt.Fprintf(tw, "tokens\tmacOS Keychain (service: indmoney-watch, account: tokens)\n")
	return tw.Flush()
}
