package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abinashstack/indmoney-watch/internal/config"
	"github.com/abinashstack/indmoney-watch/internal/indmoney"
)

// cmdMenubar prints SwiftBar plugin output — see https://github.com/swiftbar/SwiftBar
// It renders a one-line summary in the menu bar and a dropdown with details.
//
// The first non-`---` line(s) become the menu bar title (we use one short line).
// Lines after `---` populate the dropdown.
//
// `indw menubar` (no args) → print plugin output
// `indw menubar install`   → drop a wrapper script into ~/Library/Application Support/SwiftBar
func cmdMenubar(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return menubarInstall()
		default:
			return fmt.Errorf("unknown menubar subcommand: %s", args[0])
		}
	}
	return menubarRender(ctx)
}

func menubarRender(ctx context.Context) error {
	// Resolve our own absolute path once. SwiftBar's bash= actions don't
	// inherit the user shell's PATH, so we must hand them a fully-qualified
	// path or the click is a silent no-op.
	exe := selfExe()

	api, err := newAPI()
	if err != nil {
		// Don't blow up the menu bar — render an error line.
		fmt.Println("⚠ | sfimage=indianrupeesign.circle.fill color=#ef4444")
		fmt.Println("---")
		fmt.Println("Not logged in or API error | color=#ef4444")
		fmt.Printf("Login (opens browser) | bash=%s param1=login terminal=true\n", exe)
		return nil
	}
	cfg, _ := config.Load()
	mb := cfg.Menubar
	snap, err := api.NetworthSnapshot(ctx)
	if err != nil {
		fmt.Println("⚠ | sfimage=indianrupeesign.circle.fill color=#ef4444")
		fmt.Println("---")
		fmt.Printf("Snapshot failed: %s | color=#ef4444\n", sbEscape(err.Error()))
		return nil
	}

	// Menu bar title: day P&L if available, else total return %.
	totalPct := 0.0
	if snap.TotalInvested > 0 {
		totalPct = (snap.TotalCurrentValue - snap.TotalInvested) / snap.TotalInvested * 100
	}
	titleText := fmt.Sprintf("%+.2f%%", totalPct)
	// SwiftBar SF Symbol: Indian rupee sign in a filled circle. Renders as a
	// proper icon on macOS 13+. Falls back gracefully if unrecognized.
	icon := mb.SFSymbol
	if icon == "" {
		icon = "indianrupeesign.circle.fill"
	}
	fmt.Printf("%s | color=%s sfimage=%s\n", titleText, pickColor(mb, totalPct, false), icon)
	fmt.Println("---")

	// --- Portfolio summary section ---
	fmt.Printf("Net worth: ₹%s\n", commaINR(snap.TotalNetworth))
	fmt.Printf("Invested: ₹%s\n", commaINR(snap.TotalInvested))
	fmt.Printf("Current: ₹%s\n", commaINR(snap.TotalCurrentValue))
	pnl := snap.TotalCurrentValue - snap.TotalInvested
	fmt.Printf("P&L: ₹%s%s (%+.2f%%) | color=%s\n",
		sign(pnl), commaINR(absf(pnl)), totalPct, pickColor(mb, totalPct, false))

	// --- Asset class breakdown ---
	if len(snap.Investments) > 0 {
		fmt.Println("---")
		fmt.Printf("By asset class | color=%s\n", mb.HeaderColor)
		for _, inv := range snap.Investments {
			arrow := arrowFor(inv.ReturnPercentage)
			fmt.Printf("  %s %s   ₹%s   %+.2f%% | color=%s font=Menlo\n",
				arrow,
				padRight(inv.AssetType, 11),
				commaINR(inv.CurrentValue),
				inv.ReturnPercentage, pickColor(mb, inv.ReturnPercentage, false))
		}
	}

	// --- Liabilities (if any credit cards) ---
	if len(snap.Liabilities.CreditCards) > 0 {
		fmt.Println("---")
		fmt.Printf("Credit cards | color=%s\n", mb.HeaderColor)
		for _, cc := range snap.Liabilities.CreditCards {
			line := fmt.Sprintf("  %s   ₹%s   due %s",
				padRight(cc.Name, 18), commaINR(cc.TotalDue), cc.DueDate)
			fmt.Printf("%s | font=Menlo\n", sbEscape(line))
		}
	}

	// --- Watchlist (Indian) ---
	if wl, err := api.Watchlist(ctx, "indian"); err == nil {
		stocks := wl.AllStocks()
		indKeys := make([]string, 0, len(stocks))
		for _, s := range stocks {
			if s.IndKey != "" {
				indKeys = append(indKeys, s.IndKey)
			}
		}
		if len(indKeys) > 0 {
			if details, err := api.IndianStockDetails(ctx, indKeys); err == nil && len(details) > 0 {
				renderWatchlistSubmenu(mb, "Indian watchlist", "₹", details)
			}
		}
	}

	// --- Watchlist (US) ---
	// Tickers come straight from the watchlist response (server fix 2026-05-21).
	if wl, err := api.Watchlist(ctx, "us"); err == nil {
		stocks := wl.AllStocks()
		var tickers []string
		for _, s := range stocks {
			if s.Ticker != "" {
				tickers = append(tickers, s.Ticker)
			}
		}
		if len(tickers) > 0 {
			if details, err := api.USStockDetails(ctx, tickers); err == nil && len(details) > 0 {
				renderWatchlistSubmenu(mb, "US watchlist", "$", details)
			}
		}
	}

	// --- SIPs (stock + MF combined) ---
	renderSIPSection(ctx, api, mb)

	// --- Footer actions ---
	fmt.Println("---")
	fmt.Printf("Refresh | refresh=true color=%s\n", mb.HeaderColor)
	fmt.Printf("Open INDmoney | href=%s color=%s\n", mb.OpenURL, mb.HeaderColor)
	fmt.Printf("Run alert poll now | bash=%s param1=run-once terminal=false refresh=true color=%s\n", exe, mb.HeaderColor)
	fmt.Printf("Show recent alerts | bash=%s param1=logs terminal=true color=%s\n", exe, mb.HeaderColor)
	fmt.Printf("Re-login | bash=%s param1=login param2=-f terminal=true color=%s\n", exe, mb.HeaderColor)
	return nil
}

// selfExe returns the absolute path to this binary. SwiftBar's bash= actions
// run with a minimal PATH so we hand them the full path. Falls back to "indw"
// if resolution fails — better a broken click than a render error.
func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return "indw"
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return exe
	}
	return abs
}

// renderSIPSection prints a section listing all active SIPs (stock + MF).
// Silent on empty / errored — the menu bar is for at-a-glance status, not
// debugging. CLI (`indw sips`) shows errors verbosely.
func renderSIPSection(ctx context.Context, api *indmoney.API, mb config.MenubarConfig) {
	stock, _ := api.IndianStockSIPs(ctx)
	mf, _ := api.MFSIPs(ctx)
	if len(stock) == 0 && len(mf) == 0 {
		return
	}
	fmt.Println("---")
	fmt.Printf("SIPs | color=%s\n", mb.HeaderColor)
	for _, s := range stock {
		renderSIPRow(mb, "📈", s)
	}
	for _, s := range mf {
		renderSIPRow(mb, "📊", s)
	}
}

func renderSIPRow(mb config.MenubarConfig, icon string, s indmoney.SIP) {
	color := mb.PositiveColor
	statusTag := ""
	switch s.StatusKind() {
	case indmoney.StatusFailed:
		color = mb.StrongNegativeColor
		statusTag = " ⚠"
	case indmoney.StatusInProgress:
		color = "#ff9100,#ffab40" // vivid orange light/dark
		statusTag = " …"
	case indmoney.StatusUpcoming:
		color = mb.MutedColor
	}
	next := s.NextDate()
	if next == "" {
		next = "-"
	}
	line := fmt.Sprintf("  %s %s   ₹%s   next %s%s",
		icon,
		padRight(s.DisplayName(), 22),
		commaINR(s.AmountValue()),
		next,
		statusTag,
	)
	fmt.Printf("%s | color=%s font=Menlo\n", sbEscape(line), color)
}

// renderWatchlistSubmenu prints a single "Indian watchlist (10) ›" row with a
// short up/down summary, and nests the actual rows underneath via SwiftBar's
// `--` submenu prefix. Rows are sorted by symbol for stable ordering across
// polls (map iteration is random in Go).
func renderWatchlistSubmenu(mb config.MenubarConfig, title, ccy string, details map[string]indmoney.StockEntity) {
	keys := make([]string, 0, len(details))
	up, down := 0, 0
	for k, ent := range details {
		keys = append(keys, k)
		if ent.Stats.DayChangePct >= 0 {
			up++
		} else {
			down++
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return watchlistSortKey(details[keys[i]], keys[i]) < watchlistSortKey(details[keys[j]], keys[j])
	})

	fmt.Println("---")
	// Header: title in muted color, ▲ in green, ▼ in red — pulls eye to the
	// imbalance immediately. SwiftBar applies one color per *line*, so we
	// can't multi-color a single line; instead we put the tally on the same
	// line and color-code the whole header by net direction.
	netColor := mb.PositiveColor
	if down > up {
		netColor = mb.NegativeColor
	} else if down == up {
		netColor = mb.HeaderColor
	}
	header := fmt.Sprintf("%s (%d)   ▲%d ▼%d", title, len(keys), up, down)
	fmt.Printf("%s | color=%s\n", sbEscape(header), netColor)

	for _, k := range keys {
		renderWatchRow(mb, ccy, k, details[k], "--")
	}
}

func watchlistSortKey(ent indmoney.StockEntity, fallback string) string {
	if ent.Basic.Symbol != "" {
		return ent.Basic.Symbol
	}
	if ent.Basic.ShortName != "" {
		return ent.Basic.ShortName
	}
	return fallback
}

func renderWatchRow(mb config.MenubarConfig, ccy, key string, ent indmoney.StockEntity, prefix string) {
	sym := ent.Basic.Symbol
	if sym == "" {
		sym = ent.Basic.ShortName
	}
	if sym == "" {
		sym = key
	}
	pct := ent.Stats.DayChangePct
	// Submenu rows render against a solid (non-translucent) menu background,
	// so they use a separate, deeper palette tier.
	c := pickColor(mb, pct, prefix != "")
	arrow := arrowFor(pct)
	line := fmt.Sprintf("  %s %s   %s%.2f   %+.2f%%",
		arrow, padRight(sym, 10), ccy, ent.Stats.LivePrice, pct)
	// SwiftBar treats `|` as the start of attributes; pipe the line as text first.
	// `prefix` ("" for top-level, "--" / "----" for submenu nesting).
	fmt.Printf("%s%s | color=%s font=Menlo\n", prefix, sbEscape(line), c)
}

// pickColor returns the appropriate tier color for a given % move, based on
// |pct| against the configured muted/strong thresholds. When `submenu` is
// true, prefers the deeper submenu-specific colors (which read better against
// the solid menu background) and falls back to the top-level colors when
// those are unset.
//
//   |pct| < MutedMovePct   → MutedColor (quiet movement, dimmed)
//   |pct| ≥ StrongMovePct  → Strong{Pos,Neg}Color (eye-catching)
//   otherwise              → {Pos,Neg}Color (standard)
func pickColor(mb config.MenubarConfig, pct float64, submenu bool) string {
	abs := pct
	if abs < 0 {
		abs = -abs
	}
	if mb.MutedMovePct > 0 && abs < mb.MutedMovePct {
		if mb.MutedColor != "" {
			return mb.MutedColor
		}
	}
	strong := mb.StrongMovePct > 0 && abs >= mb.StrongMovePct

	pick := func(submenuColor, topColor string) string {
		if submenu && submenuColor != "" {
			return submenuColor
		}
		return topColor
	}
	switch {
	case strong && pct >= 0:
		return pick(mb.SubmenuStrongPositiveColor, mb.StrongPositiveColor)
	case strong && pct < 0:
		return pick(mb.SubmenuStrongNegativeColor, mb.StrongNegativeColor)
	case pct >= 0:
		return pick(mb.SubmenuPositiveColor, mb.PositiveColor)
	default:
		return pick(mb.SubmenuNegativeColor, mb.NegativeColor)
	}
}

// arrowFor returns a Unicode arrow indicating direction. Using filled
// triangles (▲▼) for clear visual distinction from text; lighter dash for
// near-flat to avoid implying motion.
func arrowFor(pct float64) string {
	switch {
	case pct >= 0.05:
		return "▲"
	case pct <= -0.05:
		return "▼"
	default:
		return "·"
	}
}

// menubarInstall drops a SwiftBar plugin that wraps `indw menubar`. Filename
// encodes the refresh interval (`.10m.sh`) so SwiftBar polls every 10 min.
func menubarInstall() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	// Default SwiftBar plugin folder; SwiftBar lets users override it in
	// preferences. Read the override first so the plugin lands where SwiftBar
	// actually looks. Fallback to the default Application Support path.
	pluginsDir := swiftbarPluginsDir()
	if pluginsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		pluginsDir = filepath.Join(home, "Library", "Application Support", "SwiftBar", "Plugins")
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return err
	}

	// Refresh cadence comes from config (e.g. "10m", "1s", "30s", "1h").
	cfg, _ := config.Load()
	cadence := "10m"
	if cfg != nil && cfg.Menubar.RefreshInterval != "" {
		cadence = cfg.Menubar.RefreshInterval
	}
	pluginPath := filepath.Join(pluginsDir, "indmoney."+cadence+".sh")
	script := fmt.Sprintf(`#!/bin/bash
# <bitbar.title>INDmoney</bitbar.title>
# <bitbar.version>0.1</bitbar.version>
# <bitbar.author>indmoney-watch</bitbar.author>
# <bitbar.desc>Portfolio + watchlist via INDmoney MCP</bitbar.desc>
# <swiftbar.environment>[INDW_BIN=%s]</swiftbar.environment>
exec "%s" menubar
`, exe, exe)

	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		return err
	}

	fmt.Println("Installed SwiftBar plugin:", pluginPath)
	fmt.Println()

	// Check whether SwiftBar itself is present.
	if _, err := os.Stat("/Applications/SwiftBar.app"); errors.Is(err, os.ErrNotExist) {
		fmt.Println("SwiftBar is not installed yet. Install it with:")
		fmt.Println("  brew install --cask swiftbar")
		fmt.Println("Then launch it once and point its plugin folder at:")
		fmt.Println("  ", pluginsDir)
	} else {
		fmt.Println("SwiftBar is installed. Either launch it or run:")
		fmt.Println("  open -a SwiftBar")
		_ = exec.Command("open", "-a", "SwiftBar").Start()
	}
	return nil
}

// ---- helpers ----

func sign(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// commaINR formats a number with Indian-style grouping (lakh/crore commas), 0dp.
func commaINR(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%.0f", v)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	// Last 3 digits, then groups of 2.
	last3 := s[len(s)-3:]
	rest := s[:len(s)-3]
	var groups []string
	for len(rest) > 2 {
		groups = append([]string{rest[len(rest)-2:]}, groups...)
		rest = rest[:len(rest)-2]
	}
	if rest != "" {
		groups = append([]string{rest}, groups...)
	}
	out := strings.Join(groups, ",") + "," + last3
	if neg {
		out = "-" + out
	}
	return out
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// sbEscape escapes characters SwiftBar treats specially in line text.
func sbEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "¦")
	return s
}

// swiftbarPluginsDir reads SwiftBar's user-configured plugin directory from its
// `defaults` store. Returns "" if not set. SwiftBar's bundle id is
// `com.ameba.SwiftBar` and the key is `PluginDirectory`.
func swiftbarPluginsDir() string {
	out, err := exec.Command("defaults", "read", "com.ameba.SwiftBar", "PluginDirectory").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
