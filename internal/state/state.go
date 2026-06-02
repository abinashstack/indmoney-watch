package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/abinashstack/indmoney-watch/internal/config"
)

type State struct {
	LastSnapshot      time.Time          `json:"last_snapshot"`
	PortfolioInvested float64            `json:"portfolio_invested"`
	PortfolioCurrent  float64            `json:"portfolio_current"`
	PortfolioPct      float64            `json:"portfolio_pct"`
	AssetClassPct     map[string]float64 `json:"asset_class_pct"`     // by asset_type
	HoldingPct        map[string]float64 `json:"holding_pct"`         // by symbol → return_percentage
	HoldingDayPct     map[string]float64 `json:"holding_day_pct"`     // by symbol → day_change_pct
	WatchlistPct      map[string]float64 `json:"watchlist_pct"`       // by ind_key → day_change_pct
	WatchlistLTP      map[string]float64 `json:"watchlist_ltp"`       // by ind_key → last LTP
	SIPStatus         map[string]string  `json:"sip_status"`          // by SIP key → last-seen status kind ("failed"/"upcoming"/"executed"/"in_progress")
	LastFired         map[string]time.Time `json:"last_fired"`        // alert key → time fired (debounce)
}

func New() *State {
	return &State{
		AssetClassPct: map[string]float64{},
		HoldingPct:    map[string]float64{},
		HoldingDayPct: map[string]float64{},
		WatchlistPct:  map[string]float64{},
		WatchlistLTP:  map[string]float64{},
		SIPStatus:     map[string]string{},
		LastFired:     map[string]time.Time{},
	}
}

func path() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "state.json"), nil
}

func Load() (*State, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	s := New()
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.AssetClassPct == nil {
		s.AssetClassPct = map[string]float64{}
	}
	if s.HoldingPct == nil {
		s.HoldingPct = map[string]float64{}
	}
	if s.HoldingDayPct == nil {
		s.HoldingDayPct = map[string]float64{}
	}
	if s.WatchlistPct == nil {
		s.WatchlistPct = map[string]float64{}
	}
	if s.WatchlistLTP == nil {
		s.WatchlistLTP = map[string]float64{}
	}
	if s.SIPStatus == nil {
		s.SIPStatus = map[string]string{}
	}
	if s.LastFired == nil {
		s.LastFired = map[string]time.Time{}
	}
	return s, nil
}

func Save(s *State) error {
	p, err := path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(p, b, 0o600)
}
