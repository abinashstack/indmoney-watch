package indmoney

import (
	"context"
	"strings"
)

// SIP captures the union of fields we care about across stock and MF SIPs.
// The upstream contract (per `tools/list` 2026-05-21):
//   "Returns SIP data per fund, including fund name, category, SIP amount,
//    frequency, next execution date, and status. Monthly SIP execution status:
//    upcoming, in-progress, or failed SIP installments."
//
// We don't have a live response to pin the exact field names against, so this
// struct is intentionally permissive — extra JSON fields are ignored, and the
// `Status` interpretation is centralized in StatusKind() so we only have one
// place to fix when we observe real data.
type SIP struct {
	// Identity. SIPID is the most stable per-SIP key for state tracking.
	// We fall back to a synthesized key if the server doesn't provide one.
	SIPID    string `json:"sip_id"`
	Name     string `json:"name"`
	FundName string `json:"fund_name"` // MF response often uses this
	Symbol   string `json:"symbol"`    // stock SIPs
	IndKey   string `json:"ind_key"`
	Category string `json:"category"`

	// Money + cadence.
	Amount    float64 `json:"amount"`
	SIPAmount float64 `json:"sip_amount"`
	Frequency string  `json:"frequency"`

	// Lifecycle. The server uses any of these — read all, prefer most specific.
	Status            string `json:"status"`
	InstallmentStatus string `json:"installment_status"`
	NextExecutionDate string `json:"next_execution_date"`
	NextSIPDate       string `json:"next_sip_date"`

	// Current installment block — when present, fields here override top-level.
	CurrentInstallment *Installment `json:"current_installment,omitempty"`
}

type Installment struct {
	Status      string  `json:"status"`
	Amount      float64 `json:"amount"`
	ExecutionID string  `json:"execution_id"`
	Date        string  `json:"date"`
}

// DisplayName returns whichever of the available name fields is set.
func (s *SIP) DisplayName() string {
	switch {
	case s.FundName != "":
		return s.FundName
	case s.Name != "":
		return s.Name
	case s.Symbol != "":
		return s.Symbol
	case s.IndKey != "":
		return s.IndKey
	default:
		return "(unnamed SIP)"
	}
}

// Key returns a stable identifier for this SIP for state tracking. Prefers
// SIPID if set, else falls back to a name-based key (good enough for dedupe
// across polls within a session even if the server omits an id).
func (s *SIP) Key() string {
	if s.SIPID != "" {
		return s.SIPID
	}
	return "name:" + s.DisplayName()
}

// AmountValue picks whichever amount field is populated.
func (s *SIP) AmountValue() float64 {
	if s.CurrentInstallment != nil && s.CurrentInstallment.Amount > 0 {
		return s.CurrentInstallment.Amount
	}
	if s.SIPAmount > 0 {
		return s.SIPAmount
	}
	return s.Amount
}

// NextDate picks whichever next-date field is populated.
func (s *SIP) NextDate() string {
	if s.NextExecutionDate != "" {
		return s.NextExecutionDate
	}
	return s.NextSIPDate
}

// StatusKind buckets a raw status string into one of three categories we
// alert on. The upstream uses "upcoming, in-progress, failed" per its tool
// description; we also accept common synonyms ("success", "executed", "paid").
type StatusKind int

const (
	StatusUnknown StatusKind = iota
	StatusUpcoming
	StatusInProgress
	StatusExecuted
	StatusFailed
)

func (s *SIP) StatusKind() StatusKind {
	// Prefer installment-level status, then top-level installment_status, then status.
	candidates := []string{}
	if s.CurrentInstallment != nil {
		candidates = append(candidates, s.CurrentInstallment.Status)
	}
	candidates = append(candidates, s.InstallmentStatus, s.Status)
	for _, c := range candidates {
		if k := classifyStatus(c); k != StatusUnknown {
			return k
		}
	}
	return StatusUnknown
}

func classifyStatus(raw string) StatusKind {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return StatusUnknown
	}
	switch {
	case strings.Contains(s, "fail"), strings.Contains(s, "error"), strings.Contains(s, "reject"):
		return StatusFailed
	case strings.Contains(s, "upcoming"), strings.Contains(s, "scheduled"), strings.Contains(s, "pending"):
		return StatusUpcoming
	case strings.Contains(s, "progress"), strings.Contains(s, "processing"):
		return StatusInProgress
	case strings.Contains(s, "success"), strings.Contains(s, "executed"), strings.Contains(s, "paid"), strings.Contains(s, "complete"):
		return StatusExecuted
	}
	return StatusUnknown
}

// ---- API methods ----

type stockSIPsResp struct {
	IndianStocksSIPs []SIP `json:"indian_stocks_sips"`
}

type mfSIPsResp struct {
	MFSIPs []SIP `json:"mf_sips"`
}

// IndianStockSIPs returns the user's stock SIPs. Empty slice if none.
func (a *API) IndianStockSIPs(ctx context.Context) ([]SIP, error) {
	var out stockSIPsResp
	if err := a.c.CallTool(ctx, "indian_stocks_sips", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.IndianStocksSIPs, nil
}

// MFSIPs returns the user's mutual fund SIPs. Empty slice if none.
func (a *API) MFSIPs(ctx context.Context) ([]SIP, error) {
	var out mfSIPsResp
	if err := a.c.CallTool(ctx, "mf_sips", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.MFSIPs, nil
}
