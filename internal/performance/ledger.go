package performance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"xaut-paper-bot/internal/domain"
)

type Context struct {
	Regime      domain.Regime
	Features    domain.Features
	Signal      domain.Signal
	Fair        domain.FairValue
	InitialStop float64
}

type openTrade struct {
	Record         domain.TradeRecord
	Qty            float64
	Avg            float64
	InitialRiskUSD float64
	StartFunding   float64
	Fees           float64
}

type Ledger struct {
	mu         sync.Mutex
	fillsPath  string
	tradesPath string
	statePath  string
	seen       map[int64]bool
	lastFillAt time.Time
	open       *openTrade
}

type persisted struct {
	Seen       []int64    `json:"seen"`
	LastFillAt time.Time  `json:"last_fill_at"`
	Open       *openTrade `json:"open,omitempty"`
}

func New(dataDir string) (*Ledger, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	l := &Ledger{fillsPath: filepath.Join(dataDir, "fills.jsonl"), tradesPath: filepath.Join(dataDir, "trades.jsonl"), statePath: filepath.Join(dataDir, "performance_state.json"), seen: map[int64]bool{}}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *Ledger) Since() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastFillAt.IsZero() {
		return time.Now().UTC().Add(-24 * time.Hour)
	}
	return l.lastFillAt.Add(-time.Second)
}

func (l *Ledger) Process(now time.Time, fills []domain.Fill, account domain.AccountSnapshot, ctx Context, marketPrice float64, exitReason string) ([]domain.TradeRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sort.Slice(fills, func(i, j int) bool { return fills[i].Time.Before(fills[j].Time) })
	closed := []domain.TradeRecord{}
	for _, f := range fills {
		if l.seen[f.ID] || f.ID == 0 {
			continue
		}
		l.seen[f.ID] = true
		if f.Time.After(l.lastFillAt) {
			l.lastFillAt = f.Time
		}
		if err := appendJSONL(l.fillsPath, f); err != nil {
			return closed, err
		}
		reason := exitReason
		if strings.Contains(strings.ToUpper(f.OrderType), "STOP") {
			reason = "exchange protective stop"
		}
		r := l.applyFill(f, account, ctx, reason)
		if r != nil {
			closed = append(closed, *r)
			if err := appendJSONL(l.tradesPath, *r); err != nil {
				return closed, err
			}
		}
	}
	if l.open != nil && marketPrice > 0 {
		q := l.open.Qty
		entry := l.open.Avg
		u := q * (marketPrice - entry)
		if u > l.open.Record.MFEUSD {
			l.open.Record.MFEUSD = u
		}
		if u < l.open.Record.MAEUSD {
			l.open.Record.MAEUSD = u
		}
	}
	if err := l.saveLocked(); err != nil {
		return closed, err
	}
	return closed, nil
}
func (l *Ledger) applyFill(f domain.Fill, account domain.AccountSnapshot, ctx Context, exitReason string) *domain.TradeRecord {
	a := f.Amount
	if a == 0 || f.Price <= 0 {
		return nil
	}
	if l.open == nil {
		l.start(f, account, ctx)
		return nil
	}
	o := l.open
	if sameSign(o.Qty, a) {
		old := math.Abs(o.Qty)
		add := math.Abs(a)
		o.Avg = (o.Avg*old + f.Price*add) / (old + add)
		o.Qty += a
		o.Record.Quantity = math.Abs(o.Qty)
		o.Fees += math.Abs(f.Fee)
		return nil
	}
	closeQty := math.Min(math.Abs(o.Qty), math.Abs(a))
	gross := 0.0
	if o.Qty > 0 {
		gross = closeQty * (f.Price - o.Avg)
	} else {
		gross = closeQty * (o.Avg - f.Price)
	}
	o.Record.GrossPnLUSD += gross
	o.Fees += math.Abs(f.Fee)
	remaining := o.Qty + a
	if math.Abs(remaining) > 1e-9 && !sameSign(remaining, o.Qty) { // reversal: finish then seed a new trade from excess
		r := l.finish(f, account, ctx, exitReason)
		excess := remaining
		l.open = nil
		l.start(domain.Fill{ID: f.ID, Time: f.Time, Amount: excess, Price: f.Price, Fee: 0, Maker: f.Maker, CID: f.CID, OrderID: f.OrderID, OrderType: f.OrderType, Symbol: f.Symbol}, account, ctx)
		return r
	}
	if math.Abs(remaining) <= 1e-9 {
		return l.finish(f, account, ctx, exitReason)
	}
	o.Qty = remaining
	o.Record.Quantity = math.Abs(o.Qty)
	return nil
}
func (l *Ledger) start(f domain.Fill, account domain.AccountSnapshot, ctx Context) {
	dir := "long"
	if f.Amount < 0 {
		dir = "short"
	}
	risk := math.Abs(f.Amount) * math.Abs(f.Price-ctx.InitialStop)
	l.open = &openTrade{Qty: f.Amount, Avg: f.Price, InitialRiskUSD: risk, StartFunding: account.FundingCostUSD, Fees: math.Abs(f.Fee), Record: domain.TradeRecord{ID: fmt.Sprintf("%d", f.ID), Direction: dir, Regime: ctx.Regime, EntryTime: f.Time, EntryVWAP: f.Price, Quantity: math.Abs(f.Amount), EntryBasisZ: ctx.Features.BasisZ, EntryTrend: ctx.Features.TrendScore, EntryMicro: ctx.Features.MicroScore, EntryScore: ctx.Signal.Score, FairConfidence: ctx.Fair.Confidence, InitialStop: ctx.InitialStop}}
}
func (l *Ledger) finish(f domain.Fill, account domain.AccountSnapshot, ctx Context, reason string) *domain.TradeRecord {
	o := l.open
	if o == nil {
		return nil
	}
	o.Record.ExitTime = f.Time
	o.Record.ExitVWAP = f.Price
	o.Record.ExitBasisZ = ctx.Features.BasisZ
	o.Record.FeesUSD = o.Fees
	o.Record.FundingUSD = math.Max(0, account.FundingCostUSD-o.StartFunding)
	o.Record.NetPnLUSD = o.Record.GrossPnLUSD - o.Record.FeesUSD - o.Record.FundingUSD
	if o.InitialRiskUSD > 0 {
		o.Record.RMultiple = o.Record.NetPnLUSD / o.InitialRiskUSD
	}
	o.Record.ExitReason = reason
	r := o.Record
	l.open = nil
	return &r
}
func sameSign(a, b float64) bool { return (a > 0 && b > 0) || (a < 0 && b < 0) }
func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
func (l *Ledger) load() error {
	f, err := os.Open(l.statePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var p persisted
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&p); err != nil {
		return err
	}
	for _, id := range p.Seen {
		l.seen[id] = true
	}
	l.lastFillAt = p.LastFillAt
	l.open = p.Open
	return nil
}
func (l *Ledger) saveLocked() error {
	ids := make([]int64, 0, len(l.seen))
	for id := range l.seen {
		ids = append(ids, id)
	}
	if len(ids) > 2000 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		ids = ids[len(ids)-2000:]
	}
	p := persisted{Seen: ids, LastFillAt: l.lastFillAt, Open: l.open}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.statePath)
}
