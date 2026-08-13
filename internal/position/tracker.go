package position

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

const quantityTolerance = 1e-8

type Event struct {
	ExitRequired    bool
	Reason          string
	Opened          bool
	Closed          bool
	ClosedDirection float64
	StopChanged     bool
}

type Tracker struct {
	cfg       config.RiskConfig
	exec      config.ExecutionConfig
	statePath string
	mu        sync.Mutex
	state     domain.PositionState
}

func New(cfg config.RiskConfig, exec config.ExecutionConfig, dataDir string) (*Tracker, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	t := &Tracker{cfg: cfg, exec: exec, statePath: filepath.Join(dataDir, "position_state.json")}
	if err := t.load(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Tracker) Arm(stopFraction float64) error {
	if stopFraction <= 0 || math.IsNaN(stopFraction) || math.IsInf(stopFraction, 0) {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.PendingStopFraction = math.Max(stopFraction, t.cfg.MinimumStopFraction)
	t.state.UpdatedAt = time.Now().UTC()
	return t.saveLocked()
}

func (t *Tracker) SetEntryContext(reg domain.Regime, basis, trend float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.EntryRegime = reg
	t.state.EntryBasisZ = basis
	t.state.EntryTrendScore = trend
	return t.saveLocked()
}

func (t *Tracker) Reconcile(now time.Time, account domain.AccountSnapshot, direct domain.BookSnapshot) (Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ev := Event{}
	if !direct.Valid() {
		return ev, nil
	}
	if account.SpotXAUT > quantityTolerance && account.MarginXAUT < -quantityTolerance {
		return Event{ExitRequired: true, Reason: "simultaneous spot long and margin short detected"}, nil
	}

	qty := currentQuantity(account)
	old := t.state
	if math.Abs(qty) <= quantityTolerance {
		if math.Abs(old.QuantityXAUT) > quantityTolerance {
			ev.Closed = true
			ev.ClosedDirection = old.QuantityXAUT
		}
		pending := old.PendingStopFraction
		reg, basis, trend := old.EntryRegime, old.EntryBasisZ, old.EntryTrendScore
		t.state = domain.PositionState{PendingStopFraction: pending, UpdatedAt: now, EntryRegime: reg, EntryBasisZ: basis, EntryTrendScore: trend}
		if !hasOpeningOrder(account.OpenOrders) {
			t.state.PendingStopFraction = 0
			t.state.EntryRegime = ""
			t.state.EntryBasisZ = 0
			t.state.EntryTrendScore = 0
		}
		return ev, t.saveLocked()
	}

	entry := entryPrice(account, qty, direct.Mid())
	if math.Abs(old.QuantityXAUT) <= quantityTolerance || !sameDirection(qty, old.QuantityXAUT) {
		ev.Opened = true
		t.openLocked(now, qty, entry, t.stopFractionLocked())
	} else {
		oldStop := t.state.StopPrice
		oldAbs, newAbs := math.Abs(old.QuantityXAUT), math.Abs(qty)
		if newAbs > oldAbs+quantityTolerance {
			if qty < 0 && account.MarginBasePrice > 0 {
				t.state.AverageEntry = account.MarginBasePrice
			} else {
				t.state.AverageEntry = (old.AverageEntry*oldAbs + direct.Mid()*(newAbs-oldAbs)) / newAbs
			}
			t.state.InitialStopFraction = math.Max(t.state.InitialStopFraction, t.stopFractionLocked())
			t.tightenInitialStopLocked(qty)
			t.state.PendingStopFraction = 0
		}
		t.state.QuantityXAUT = qty
		t.state.UpdatedAt = now
		if math.Abs(oldStop-t.state.StopPrice) > 1e-9 {
			ev.StopChanged = true
		}
	}

	oldStop := t.state.StopPrice
	t.updateBestAndTrailLocked(direct.Mid())
	if math.Abs(oldStop-t.state.StopPrice) > 1e-9 {
		ev.StopChanged = true
	}
	t.state.ExchangeStopOrderID = findStopOrder(account.OpenOrders, t.exec.StopGroupID)
	if r := t.exitReasonLocked(now, direct); r != "" {
		ev.ExitRequired = true
		ev.Reason = r
	}
	return ev, t.saveLocked()
}

func (t *Tracker) State() domain.PositionState { t.mu.Lock(); defer t.mu.Unlock(); return t.state }

func (t *Tracker) openLocked(now time.Time, qty, entry, stopFrac float64) {
	stopFrac = math.Max(stopFrac, t.cfg.MinimumStopFraction)
	reg, basis, trend := t.state.EntryRegime, t.state.EntryBasisZ, t.state.EntryTrendScore
	t.state = domain.PositionState{QuantityXAUT: qty, AverageEntry: entry, InitialStopFraction: stopFrac, BestPrice: entry, OpenedAt: now, UpdatedAt: now, EntryRegime: reg, EntryBasisZ: basis, EntryTrendScore: trend}
	if qty > 0 {
		t.state.StopPrice = entry * (1 - stopFrac)
	} else {
		t.state.StopPrice = entry * (1 + stopFrac)
	}
}

func (t *Tracker) tightenInitialStopLocked(qty float64) {
	if t.state.AverageEntry <= 0 || t.state.InitialStopFraction <= 0 {
		return
	}
	if qty > 0 {
		c := t.state.AverageEntry * (1 - t.state.InitialStopFraction)
		if t.state.StopPrice <= 0 || c > t.state.StopPrice {
			t.state.StopPrice = c
		}
	} else {
		c := t.state.AverageEntry * (1 + t.state.InitialStopFraction)
		if t.state.StopPrice <= 0 || c < t.state.StopPrice {
			t.state.StopPrice = c
		}
	}
}

func (t *Tracker) updateBestAndTrailLocked(price float64) {
	if math.Abs(t.state.QuantityXAUT) <= quantityTolerance || t.state.AverageEntry <= 0 {
		return
	}
	if t.state.QuantityXAUT > 0 {
		if price > t.state.BestPrice {
			t.state.BestPrice = price
		}
	} else if t.state.BestPrice <= 0 || price < t.state.BestPrice {
		t.state.BestPrice = price
	}
	rd := t.state.AverageEntry * t.state.InitialStopFraction
	if rd <= 0 || t.cfg.TrailingActivationR <= 0 || t.cfg.TrailingDistanceR <= 0 {
		return
	}
	if t.state.QuantityXAUT > 0 {
		r := (t.state.BestPrice - t.state.AverageEntry) / rd
		if r >= t.cfg.TrailingActivationR {
			c := t.state.BestPrice - rd*t.cfg.TrailingDistanceR
			if c > t.state.StopPrice {
				t.state.StopPrice = c
			}
		}
	} else {
		r := (t.state.AverageEntry - t.state.BestPrice) / rd
		if r >= t.cfg.TrailingActivationR {
			c := t.state.BestPrice + rd*t.cfg.TrailingDistanceR
			if t.state.StopPrice <= 0 || c < t.state.StopPrice {
				t.state.StopPrice = c
			}
		}
	}
}

func (t *Tracker) exitReasonLocked(now time.Time, d domain.BookSnapshot) string {
	if math.Abs(t.state.QuantityXAUT) <= quantityTolerance || t.state.StopPrice <= 0 {
		return ""
	}
	if t.state.QuantityXAUT > 0 && d.Bid <= t.state.StopPrice {
		return fmt.Sprintf("software backup stop: bid %.2f <= %.2f", d.Bid, t.state.StopPrice)
	}
	if t.state.QuantityXAUT < 0 && d.Ask >= t.state.StopPrice {
		return fmt.Sprintf("software backup stop: ask %.2f >= %.2f", d.Ask, t.state.StopPrice)
	}
	if t.cfg.MaximumHoldingTime.Duration > 0 && !t.state.OpenedAt.IsZero() && now.Sub(t.state.OpenedAt) >= t.cfg.MaximumHoldingTime.Duration {
		return "maximum holding time reached"
	}
	return ""
}

func (t *Tracker) stopFractionLocked() float64 {
	if t.state.PendingStopFraction > 0 {
		return math.Max(t.state.PendingStopFraction, t.cfg.MinimumStopFraction)
	}
	return t.cfg.MinimumStopFraction
}
func currentQuantity(a domain.AccountSnapshot) float64 {
	if a.SpotXAUT > quantityTolerance {
		return a.SpotXAUT
	}
	if a.MarginXAUT < -quantityTolerance {
		return a.MarginXAUT
	}
	return 0
}
func entryPrice(a domain.AccountSnapshot, q, market float64) float64 {
	if q < 0 && a.MarginBasePrice > 0 {
		return a.MarginBasePrice
	}
	return market
}
func sameDirection(a, b float64) bool { return (a > 0 && b > 0) || (a < 0 && b < 0) }
func hasOpeningOrder(os []domain.OpenOrder) bool {
	for _, o := range os {
		if (o.Venue == domain.VenueSpot && o.RemainingAmount > quantityTolerance) || (o.Venue == domain.VenueMargin && o.RemainingAmount < -quantityTolerance) {
			return true
		}
	}
	return false
}
func findStopOrder(os []domain.OpenOrder, gid int64) int64 {
	for _, o := range os {
		if o.GID == gid {
			return o.ID
		}
	}
	return 0
}
func (t *Tracker) load() error {
	b, err := os.ReadFile(t.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &t.state)
}
func (t *Tracker) saveLocked() error {
	b, err := json.MarshalIndent(t.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.statePath)
}
