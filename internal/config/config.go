package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Target struct {
	Above float64 `yaml:"above,omitempty" json:"above,omitempty"`
	Below float64 `yaml:"below,omitempty" json:"below,omitempty"`
}

type Config struct {
	// Thresholds in percent (e.g. 1.0 means 1%).
	PortfolioSwingPct        float64 `yaml:"portfolio_swing_pct"`
	AssetClassSwingPct       float64 `yaml:"asset_class_swing_pct"`
	HoldingDaySwingPct       float64 `yaml:"holding_day_swing_pct"`
	HoldingTotalSwingPct     float64 `yaml:"holding_total_swing_pct"`
	WatchlistDaySwingPct     float64 `yaml:"watchlist_day_swing_pct"`
	CreditCardDueWarningDays int     `yaml:"credit_card_due_warning_days"`

	// SIP alerts. SIPDueWarningDays mirrors credit-card behavior: warn when
	// next_execution_date is within N days. Set to 0 to disable due alerts.
	// Failed alerts are always on when sip_alerts_enabled is true.
	// Executed alerts confirm a successful installment — off by default to
	// avoid noise.
	SIPAlertsEnabled    bool `yaml:"sip_alerts_enabled"`
	SIPDueWarningDays   int  `yaml:"sip_due_warning_days"`
	SIPExecutedAlerts   bool `yaml:"sip_executed_alerts"`

	// Watchlist price targets keyed by ind_key (preferred) or symbol.
	Targets map[string]Target `yaml:"targets"`

	// Debounce — don't re-fire the same alert key within this many minutes.
	DebounceMinutes int `yaml:"debounce_minutes"`

	// Menubar (SwiftBar plugin) appearance. All optional — sensible defaults applied.
	Menubar MenubarConfig `yaml:"menubar"`
}

// MenubarConfig controls how `indw menubar` renders. Every field is optional;
// Defaults() fills in reasonable values so you never have to set this section.
type MenubarConfig struct {
	// Decimal places used in the bar title (e.g. 1 → "+1.2%", 2 → "+1.24%").
	TitleDecimals int `yaml:"title_decimals"`
	// Decimal places used inside dropdown rows.
	RowDecimals int `yaml:"row_decimals"`
	// SF Symbol name for the menu bar icon. See Apple's "SF Symbols" app.
	// Examples: "indianrupeesign.circle.fill", "chart.line.uptrend.xyaxis".
	// Set to empty string to render no icon.
	SFSymbol string `yaml:"sf_symbol"`
	// Color names for positive / negative values. Any SwiftBar-recognized
	// color works: named (red/green/orange/…), hex ("#22c55e"), or dual
	// light,dark form ("#16a34a,#22c55e") which adapts to menu bar appearance.
	PositiveColor string `yaml:"positive_color"`
	NegativeColor string `yaml:"negative_color"`
	// Strong-move tier — used when |day%| ≥ StrongMovePct. Same syntax as above.
	// Lets large moves stand out from quiet ones without making everything loud.
	StrongPositiveColor string  `yaml:"strong_positive_color"`
	StrongNegativeColor string  `yaml:"strong_negative_color"`
	StrongMovePct       float64 `yaml:"strong_move_pct"`
	// Submenu colors — used for rows inside hover-expand submenus (e.g. the
	// watchlist details). Submenus render against a solid menu background, not
	// the translucent menu bar, so they need deeper/more saturated colors to
	// stay readable. Falls back to the top-level *Color values when empty.
	SubmenuPositiveColor       string `yaml:"submenu_positive_color"`
	SubmenuNegativeColor       string `yaml:"submenu_negative_color"`
	SubmenuStrongPositiveColor string `yaml:"submenu_strong_positive_color"`
	SubmenuStrongNegativeColor string `yaml:"submenu_strong_negative_color"`
	// Muted tier — used when |day%| < MutedMovePct. Default leaves small
	// movements visually quiet so the eye is drawn to real changes.
	MutedColor    string  `yaml:"muted_color"`
	MutedMovePct  float64 `yaml:"muted_move_pct"`
	// Section header color (Indian watchlist / US watchlist headers).
	HeaderColor string `yaml:"header_color"`
	// SwiftBar plugin refresh cadence, e.g. "10m", "5m", "1h".
	// Used as the file extension when `indw menubar install` writes the script.
	RefreshInterval string `yaml:"refresh_interval"`
	// URL opened by the "Open INDmoney" footer action.
	OpenURL string `yaml:"open_url"`
}

func Defaults() *Config {
	return &Config{
		PortfolioSwingPct:        1.0,
		AssetClassSwingPct:       2.0,
		HoldingDaySwingPct:       3.0,
		HoldingTotalSwingPct:     5.0,
		WatchlistDaySwingPct:     3.0,
		CreditCardDueWarningDays: 3,
		SIPAlertsEnabled:         true,
		SIPDueWarningDays:        2,
		SIPExecutedAlerts:        false,
		Targets:                  map[string]Target{},
		DebounceMinutes:          120,
		Menubar: MenubarConfig{
			TitleDecimals: 2,
			RowDecimals:   2,
			SFSymbol:      "indianrupeesign.circle.fill",
			// Vivid trading palette — designed to stay punchy through the menu
			// bar's translucent material. Light/dark dual-tones via SwiftBar's
			// "light,dark" hex syntax. Standard tier uses Robinhood-ish bright
			// emerald/crimson; strong tier goes near-fluorescent.
			// Pure-channel neon: maxed-out single-channel hex (#00ff00 / #ff0000
			// family) survives the menu bar's translucent wash where Tailwind /
			// material palettes get bleached. Light-mode variants lean cyan/magenta
			// for extra pop on white backgrounds.
			PositiveColor:       "#00d100,#39ff14", // pure green light, neon green dark
			NegativeColor:       "#ff0033,#ff3838", // racing red light, hot red dark
			StrongPositiveColor: "#00a000,#00ff66", // saturated green → electric mint
			StrongNegativeColor: "#cc0000,#ff0033", // pure red → neon red
			StrongMovePct:       2.0,
			// Submenu palette — submenus render against a solid background, so
			// they take dense darks on light mode (high contrast on white) and
			// blazing neons on dark mode.
			SubmenuPositiveColor:       "#006400,#39ff14", // forest dark / neon green
			SubmenuNegativeColor:       "#a00000,#ff3838", // deep red / hot red
			SubmenuStrongPositiveColor: "#004d00,#39ff14", // jungle green / neon green
			SubmenuStrongNegativeColor: "#7a0000,#ff0033", // blood / neon red
			// Muted tier disabled by default (set MutedMovePct=0). The menu
			// bar's translucency already washes small movements; dimming them
			// further makes them invisible. Uncomment to re-enable.
			MutedColor:   "#94a3b8,#cbd5e1", // slate-400/300 — only if user opts back in
			MutedMovePct: 0,
			HeaderColor:  "#64748b,#e2e8f0", // slate-500 / slate-200 — clearly readable
			RefreshInterval: "10m",
			OpenURL:         "https://www.indmoney.com",
		},
	}
}

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".config", "indmoney-watch")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

func path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		c := Defaults()
		if err := Save(c); err != nil {
			return nil, err
		}
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	c := Defaults()
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	return c, nil
}

func Save(c *Config) error {
	p, err := path()
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// Pretty-print Targets for CLI status output.
func (c *Config) TargetsJSON() string {
	b, _ := json.MarshalIndent(c.Targets, "", "  ")
	return string(b)
}
