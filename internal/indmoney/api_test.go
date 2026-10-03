package indmoney

import "testing"

func TestAllStocksKeepsTickerOnlyEntries(t *testing.T) {
	w := &Watchlist{Watchlists: []WatchlistGroup{
		{Stocks: []WatchlistEntry{{Ticker: "AAPL"}, {Ticker: "MSFT"}, {IndKey: "INE1"}}},
		{Stocks: []WatchlistEntry{{Ticker: "AAPL"}, {IndKey: "INE1"}, {}}},
	}}
	got := w.AllStocks()
	if len(got) != 4 { // AAPL, MSFT, INE1, and the keyless entry
		t.Fatalf("AllStocks returned %d entries: %+v", len(got), got)
	}
}
