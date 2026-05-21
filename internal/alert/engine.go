package alert

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/config"
	"github.com/abinashstack/indmoney-watch/internal/indmoney"
	"github.com/abinashstack/indmoney-watch/internal/notify"
	"github.com/abinashstack/indmoney-watch/internal/state"
)

type Engine struct {
	api    *indmoney.API
	cfg    *config.Config
	st     *state.State
	notify func(title, subtitle, msg string) error
}

func NewEngine(api *indmoney.API, cfg *config.Config, st *state.State) *Engine {
	return &Engine{api: api, cfg: cfg, st: st, notify: notify.MacBanner}
}

func (e *Engine) Run(ctx context.Context) error {
	now := time.Now()

	// 1. Snapshot
	snap, err := e.api.NetworthSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	// Portfolio total return percentage (vs cost — we don't have intraday total
	// from the snapshot, so we alert when this crosses thresholds vs last poll).
	totalPct := pctReturn(snap.TotalCurrentValue, snap.TotalInvested)
	if e.st.PortfolioInvested > 0 {
		delta := totalPct - e.st.PortfolioPct
		if math.Abs(delta) >= e.cfg.PortfolioSwingPct {
			e.fire(now, "portfolio:total",
				"Portfolio moved",
				fmt.Sprintf("%+.2f%% since last check", delta),
				fmt.Sprintf("Now %+.2f%% (₹%.0f → ₹%.0f)",
					totalPct, snap.TotalInvested, snap.TotalCurrentValue),
			)
		}
	}
	e.st.PortfolioInvested = snap.TotalInvested
	e.st.PortfolioCurrent = snap.TotalCurrentValue
	e.st.PortfolioPct = totalPct

	// 2. Asset-class swings (by asset_type).
	for _, inv := range snap.Investments {
		prev, ok := e.st.AssetClassPct[inv.AssetType]
		curr := inv.ReturnPercentage
		if ok {
			delta := curr - prev
			if math.Abs(delta) >= e.cfg.AssetClassSwingPct {
				e.fire(now, "assetclass:"+inv.AssetType,
					fmt.Sprintf("%s moved", inv.AssetType),
					fmt.Sprintf("%+.2f%% since last check", delta),
					fmt.Sprintf("Now %+.2f%% (₹%.0f current)", curr, inv.CurrentValue),
				)
			}
		}
		e.st.AssetClassPct[inv.AssetType] = curr
	}

	// 3. Per-holding swings (only asset types with non-zero allocation).
	for _, inv := range snap.Investments {
		if inv.CurrentValue == 0 {
			continue
		}
		hr, err := e.api.Holdings(ctx, inv.AssetType)
		if err != nil {
			continue // tolerate per-asset failures
		}
		for _, h := range hr.Holdings {
			key := h.Symbol
			if key == "" {
				key = h.IndKey
			}
			if key == "" {
				continue
			}
			// Day swing.
			if math.Abs(h.DayChangePct) >= e.cfg.HoldingDaySwingPct {
				prev := e.st.HoldingDayPct[key]
				if math.Abs(h.DayChangePct-prev) >= e.cfg.HoldingDaySwingPct/2 {
					e.fire(now, "holding-day:"+key,
						fmt.Sprintf("%s %+.2f%% today", display(h), h.DayChangePct),
						fmt.Sprintf("LTP ₹%.2f", h.LTP),
						fmt.Sprintf("Day change ₹%+.2f, position ₹%.0f", h.DayChange, h.CurrentValue),
					)
				}
			}
			// Total return swing vs cost.
			prevTot, ok := e.st.HoldingPct[key]
			if ok && math.Abs(h.ReturnPercentage-prevTot) >= e.cfg.HoldingTotalSwingPct {
				e.fire(now, "holding-total:"+key,
					fmt.Sprintf("%s %+.2f%% (cost basis)", display(h), h.ReturnPercentage),
					fmt.Sprintf("LTP ₹%.2f", h.LTP),
					fmt.Sprintf("Was %+.2f%%, now %+.2f%%", prevTot, h.ReturnPercentage),
				)
			}
			e.st.HoldingDayPct[key] = h.DayChangePct
			e.st.HoldingPct[key] = h.ReturnPercentage
		}
	}

	// 4 & 5. Watchlist — Indian via user_watchlist + get_indian_stocks_details;
	// US via config.USWatchlist + get_us_stocks_details.
	e.checkIndianWatchlist(ctx, now)
	e.checkUSWatchlist(ctx, now)

	// 6. Credit-card due-date warnings.
	if e.cfg.CreditCardDueWarningDays > 0 {
		for _, cc := range snap.Liabilities.CreditCards {
			if cc.DueDate == "" {
				continue
			}
			due, err := time.Parse("2006-01-02", cc.DueDate)
			if err != nil {
				continue
			}
			daysLeft := int(math.Floor(time.Until(due).Hours() / 24))
			if daysLeft >= 0 && daysLeft <= e.cfg.CreditCardDueWarningDays {
				e.fire(now, "cc-due:"+cc.Name+":"+cc.DueDate,
					fmt.Sprintf("%s due in %dd", cc.Name, daysLeft),
					fmt.Sprintf("₹%.2f due %s", cc.TotalDue, cc.DueDate),
					fmt.Sprintf("Statement %s, limit ₹%.0f", cc.StatementDate, cc.CreditLimit),
				)
			}
		}
	}

	// 7. SIP alerts (failed / due-soon / optionally executed).
	if e.cfg.SIPAlertsEnabled {
		e.checkSIPs(ctx, now)
	}

	e.st.LastSnapshot = now
	return state.Save(e.st)
}

func (e *Engine) checkIndianWatchlist(ctx context.Context, now time.Time) {
	wl, err := e.api.Watchlist(ctx, "indian")
	if err != nil {
		return
	}
	stocks := wl.AllStocks()
	if len(stocks) == 0 {
		return
	}
	indKeys := make([]string, 0, len(stocks))
	for _, s := range stocks {
		indKeys = append(indKeys, s.IndKey)
	}
	details, err := e.api.IndianStockDetails(ctx, indKeys)
	if err != nil {
		return
	}
	for indKey, ent := range details {
		e.applyWatchlistRow(now, "₹", indKey, ent)
	}
}

func (e *Engine) checkUSWatchlist(ctx context.Context, now time.Time) {
	wl, err := e.api.Watchlist(ctx, "us")
	if err != nil {
		return
	}
	stocks := wl.AllStocks()
	if len(stocks) == 0 {
		return
	}
	// Tickers come straight from the watchlist response now (server-side fix
	// shipped 2026-05-21). No more cfg.USIDMap lookup required.
	var tickers []string
	for _, s := range stocks {
		if s.Ticker != "" {
			tickers = append(tickers, s.Ticker)
		}
	}
	if len(tickers) == 0 {
		return
	}
	details, err := e.api.USStockDetails(ctx, tickers)
	if err != nil {
		return
	}
	for tkr, ent := range details {
		e.applyWatchlistRow(now, "$", tkr, ent)
	}
}

func (e *Engine) applyWatchlistRow(now time.Time, ccy, key string, ent indmoney.StockEntity) {
	sym := ent.Basic.Symbol
	if sym == "" {
		sym = ent.Basic.ShortName
	}
	if sym == "" {
		sym = key
	}
	stats := ent.Stats

	// Targets — try the key (ind_key for IND, ticker for US) and the symbol.
	if t, ok := e.cfg.Targets[key]; ok {
		e.checkTargetRow(now, ccy, key, sym, stats.LivePrice, stats.DayChangePct, t)
	}
	if sym != key {
		if t, ok := e.cfg.Targets[sym]; ok {
			e.checkTargetRow(now, ccy, key, sym, stats.LivePrice, stats.DayChangePct, t)
		}
	}

	// Day swing.
	if math.Abs(stats.DayChangePct) >= e.cfg.WatchlistDaySwingPct {
		prev := e.st.WatchlistPct[key]
		if math.Abs(stats.DayChangePct-prev) >= e.cfg.WatchlistDaySwingPct/2 {
			e.fire(now, "watchlist-day:"+key,
				fmt.Sprintf("[Watchlist] %s %+.2f%%", sym, stats.DayChangePct),
				fmt.Sprintf("LTP %s%.2f", ccy, stats.LivePrice),
				fmt.Sprintf("Day change %s%+.2f", ccy, stats.DayChange),
			)
		}
	}
	e.st.WatchlistPct[key] = stats.DayChangePct
	e.st.WatchlistLTP[key] = stats.LivePrice
}

// checkSIPs polls both SIP tools and fires alerts on status transitions and
// upcoming due dates. Alerts are debounced via fire(); status-transition
// alerts also use sticky state so the same "failed" doesn't refire even if
// it falls outside the debounce window. Each tool failure is tolerated
// independently — a stock-SIP API hiccup shouldn't suppress MF SIP alerts.
func (e *Engine) checkSIPs(ctx context.Context, now time.Time) {
	stock, err := e.api.IndianStockSIPs(ctx)
	if err != nil {
		fmt.Printf("[sip] stock fetch failed: %v\n", err)
	} else {
		e.applySIPs(now, "stock", stock)
	}
	mf, err := e.api.MFSIPs(ctx)
	if err != nil {
		fmt.Printf("[sip] mf fetch failed: %v\n", err)
	} else {
		e.applySIPs(now, "mf", mf)
	}
}

func (e *Engine) applySIPs(now time.Time, kind string, sips []indmoney.SIP) {
	for _, sip := range sips {
		key := kind + ":" + sip.Key()
		curr := sipStatusName(sip.StatusKind())
		prev := e.st.SIPStatus[key]
		name := sip.DisplayName()
		amt := sip.AmountValue()
		nextDate := sip.NextDate()

		// 1. Failed — fire on entry into failed state.
		if curr == "failed" && prev != "failed" {
			e.fire(now, "sip-failed:"+key,
				fmt.Sprintf("⚠ SIP failed: %s", name),
				fmt.Sprintf("₹%.0f installment failed", amt),
				fmt.Sprintf("Check INDmoney to retry — %s SIP", kind),
			)
		}

		// 2. Executed — only if user opted in; fire on transition to executed.
		if e.cfg.SIPExecutedAlerts && curr == "executed" && prev != "executed" {
			e.fire(now, "sip-executed:"+key,
				fmt.Sprintf("✓ SIP executed: %s", name),
				fmt.Sprintf("₹%.0f invested", amt),
				fmt.Sprintf("Next: %s", nextDate),
			)
		}

		// 3. Due-soon — fire when next_execution_date is within the window.
		// Date-scoped alert key so it fires once per installment cycle.
		if e.cfg.SIPDueWarningDays > 0 && nextDate != "" {
			if due, err := time.Parse("2006-01-02", nextDate); err == nil {
				daysLeft := int(math.Floor(time.Until(due).Hours() / 24))
				if daysLeft >= 0 && daysLeft <= e.cfg.SIPDueWarningDays {
					e.fire(now, "sip-due:"+key+":"+nextDate,
						fmt.Sprintf("SIP %s due in %dd", name, daysLeft),
						fmt.Sprintf("₹%.0f on %s", amt, nextDate),
						fmt.Sprintf("%s SIP — %s", kind, sip.Frequency),
					)
				}
			}
		}

		if curr != "" {
			e.st.SIPStatus[key] = curr
		}
	}
}

func sipStatusName(k indmoney.StatusKind) string {
	switch k {
	case indmoney.StatusFailed:
		return "failed"
	case indmoney.StatusUpcoming:
		return "upcoming"
	case indmoney.StatusInProgress:
		return "in_progress"
	case indmoney.StatusExecuted:
		return "executed"
	}
	return ""
}

func (e *Engine) checkTargetRow(now time.Time, ccy, key, sym string, ltp, dayPct float64, t config.Target) {
	if t.Above > 0 && ltp >= t.Above {
		e.fire(now, "target-above:"+key,
			fmt.Sprintf("[Target] %s ≥ %s%.2f", sym, ccy, t.Above),
			fmt.Sprintf("LTP %s%.2f", ccy, ltp),
			fmt.Sprintf("Day %+.2f%%", dayPct),
		)
	}
	if t.Below > 0 && ltp <= t.Below {
		e.fire(now, "target-below:"+key,
			fmt.Sprintf("[Target] %s ≤ %s%.2f", sym, ccy, t.Below),
			fmt.Sprintf("LTP %s%.2f", ccy, ltp),
			fmt.Sprintf("Day %+.2f%%", dayPct),
		)
	}
}

// fire emits a notification with debouncing — same key won't re-fire within DebounceMinutes.
func (e *Engine) fire(now time.Time, key, title, subtitle, msg string) {
	if last, ok := e.st.LastFired[key]; ok {
		if now.Sub(last) < time.Duration(e.cfg.DebounceMinutes)*time.Minute {
			return
		}
	}
	e.st.LastFired[key] = now
	if err := e.notify(title, subtitle, msg); err != nil {
		fmt.Printf("[notify error] %v: %s — %s\n", err, title, msg)
	} else {
		fmt.Printf("[alert] %s | %s | %s\n", title, subtitle, msg)
	}
}

func pctReturn(curr, inv float64) float64 {
	if inv == 0 {
		return 0
	}
	return (curr - inv) / inv * 100
}

func display(h indmoney.Holding) string {
	if h.Symbol != "" {
		return h.Symbol
	}
	if h.Name != "" {
		return h.Name
	}
	return h.IndKey
}
