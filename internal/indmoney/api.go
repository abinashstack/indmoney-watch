package indmoney

import (
	"context"

	"github.com/abinashstack/indmoney-watch/internal/mcpclient"
)

// API wraps the IndMoney MCP tools we actually use.
type API struct{ c *mcpclient.Client }

func New(c *mcpclient.Client) *API { return &API{c: c} }

// ---- networth_snapshot ----

type AssetTypeRow struct {
	AssetType        string  `json:"asset_type"`
	InvestedValue    float64 `json:"invested_value"`
	CurrentValue     float64 `json:"current_value"`
	Return           float64 `json:"return"`
	ReturnPercentage float64 `json:"return_percentage"`
}

type AssetClassRow struct {
	AssetclassL2     string  `json:"assetclass_l2"`
	InvestedValue    float64 `json:"invested_value"`
	CurrentValue     float64 `json:"current_value"`
	Return           float64 `json:"return"`
	ReturnPercentage float64 `json:"return_percentage"`
}

type CreditCard struct {
	Name          string  `json:"name"`
	CardVariant   string  `json:"card_variant"`
	TotalDue      float64 `json:"total_due"`
	CreditLimit   float64 `json:"credit_limit"`
	DueDate       string  `json:"due_date"`        // YYYY-MM-DD
	StatementDate string  `json:"statement_date"`
}

type Liabilities struct {
	TotalLoanBalance   float64      `json:"total_loan_balance"`
	TotalCreditCardDue float64      `json:"total_credit_card_due"`
	Total              float64      `json:"total"`
	CreditCards        []CreditCard `json:"credit_cards"`
}

type NetworthSnapshot struct {
	TotalInvested     float64         `json:"total_invested"`
	TotalCurrentValue float64         `json:"total_current_value"`
	TotalNetworth     float64         `json:"total_networth"`
	Investments       []AssetTypeRow  `json:"investments"`
	Assets            []AssetClassRow `json:"assets"`
	Liabilities       Liabilities     `json:"liabilities"`
}

func (a *API) NetworthSnapshot(ctx context.Context) (*NetworthSnapshot, error) {
	var out NetworthSnapshot
	if err := a.c.CallTool(ctx, "networth_snapshot", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- networth_holdings ----

// Holding fields are conservative; only what we use. Extra JSON fields are ignored.
type Holding struct {
	Symbol           string  `json:"symbol"`
	Name             string  `json:"name"`
	Quantity         float64 `json:"quantity"`
	AvgPrice         float64 `json:"avg_price"`
	LTP              float64 `json:"ltp"`
	CurrentValue     float64 `json:"current_value"`
	InvestedValue    float64 `json:"invested_value"`
	Return           float64 `json:"return"`
	ReturnPercentage float64 `json:"return_percentage"`
	DayChange        float64 `json:"day_change"`
	DayChangePct     float64 `json:"day_change_percentage"`
	IndKey           string  `json:"ind_key"`
}

type HoldingsResp struct {
	Holdings []Holding `json:"holdings"`
}

func (a *API) Holdings(ctx context.Context, assetType string) (*HoldingsResp, error) {
	var out HoldingsResp
	if err := a.c.CallTool(ctx, "networth_holdings", map[string]any{"asset_type": assetType}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- user_watchlist ----

// WatchlistEntry is one stock in a watchlist. Ticker may be empty for Indian
// indices (e.g. NIFTY/SENSEX have only an ind_key) — callers must tolerate it.
type WatchlistEntry struct {
	IndKey string `json:"ind_key"`
	Ticker string `json:"ticker"`
}

// WatchlistGroup is one named watchlist (a user can have several).
type WatchlistGroup struct {
	Name        string           `json:"name"`
	WatchlistID int64            `json:"watchlist_id"`
	Stocks      []WatchlistEntry `json:"stocks"`
}

// Watchlist is the response shape from `user_watchlist` (post-2026-05-21
// server fix). Old `{ids: []}` shape is no longer returned.
type Watchlist struct {
	Type       string           `json:"type"`
	Watchlists []WatchlistGroup `json:"watchlists"`
}

// AllStocks flattens entries across every named watchlist, deduplicating by
// ind_key (a stock can appear in multiple watchlists).
func (w *Watchlist) AllStocks() []WatchlistEntry {
	seen := map[string]bool{}
	var out []WatchlistEntry
	for _, g := range w.Watchlists {
		for _, s := range g.Stocks {
			if seen[s.IndKey] {
				continue
			}
			seen[s.IndKey] = true
			out = append(out, s)
		}
	}
	return out
}

func (a *API) Watchlist(ctx context.Context, kind string) (*Watchlist, error) {
	if kind == "" {
		kind = "all"
	}
	var out Watchlist
	if err := a.c.CallTool(ctx, "user_watchlist", map[string]any{"type": kind}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- get_indian_stocks_details ----

type EntityBasic struct {
	IndKey      string `json:"ind_key"`
	Name        string `json:"name"`
	ShortName   string `json:"short_name"`
	DisplayName string `json:"display_name"`
	Symbol      string `json:"symbol"`
	Exchange    string `json:"exchange"`
	MarketCap   string `json:"market_cap"`
	IsActive    bool   `json:"is_active"`
}

type EntityStats struct {
	LivePrice         float64 `json:"live_price"`
	DayChange         float64 `json:"day_change"`
	DayChangePct      float64 `json:"day_change_percentage"`
	PrevClose         float64 `json:"prev_close"`
	DayLow            float64 `json:"day_low"`
	DayHigh           float64 `json:"day_high"`
	DayOpen           float64 `json:"day_open"`
	Week52High        float64 `json:"52week_high"`
	Week52Low         float64 `json:"52week_low"`
	LastUpdated       string  `json:"last_updated"`
	Volume            float64 `json:"volume"`
}

type StockEntity struct {
	EntityClass string      `json:"entity_class"`
	Basic       EntityBasic `json:"entity_basic"`
	Stats       EntityStats `json:"entity_stats"`
}

// IndianStockDetails returns ind_key → details. The upstream caps each call at
// 10 ind_keys, so we chunk transparently.
func (a *API) IndianStockDetails(ctx context.Context, indKeys []string) (map[string]StockEntity, error) {
	if len(indKeys) == 0 {
		return map[string]StockEntity{}, nil
	}
	merged := map[string]StockEntity{}
	for _, batch := range chunk(indKeys, 10) {
		var out map[string]StockEntity
		if err := a.c.CallTool(ctx, "get_indian_stocks_details", map[string]any{
			"ind_keys": batch,
		}, &out); err != nil {
			return nil, err
		}
		for k, v := range out {
			merged[k] = v
		}
	}
	return merged, nil
}

// USStockDetails returns symbol → details. Uses the same Entity shape.
// The upstream caps each call at 10 symbols, so we chunk transparently.
// May return service_error from upstream — caller should tolerate.
func (a *API) USStockDetails(ctx context.Context, symbols []string) (map[string]StockEntity, error) {
	if len(symbols) == 0 {
		return map[string]StockEntity{}, nil
	}
	merged := map[string]StockEntity{}
	for _, batch := range chunk(symbols, 10) {
		var out map[string]StockEntity
		if err := a.c.CallTool(ctx, "get_us_stocks_details", map[string]any{
			"symbols": batch,
		}, &out); err != nil {
			return nil, err
		}
		for k, v := range out {
			merged[k] = v
		}
	}
	return merged, nil
}

func chunk(s []string, n int) [][]string {
	if n <= 0 || len(s) <= n {
		return [][]string{s}
	}
	var out [][]string
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}
