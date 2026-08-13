package bitfinex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/book"
	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/common"
	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/order"
	"github.com/bitfinexcom/bitfinex-api-go/v2/rest"
	"github.com/bitfinexcom/bitfinex-api-go/v2/websocket"

	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
	"xaut-paper-bot/internal/exchange"
)

// PaperOnlyBuild is intentionally a compile-time constant. This adapter has no
// setting that permits real-account order submission.
const PaperOnlyBuild = true

type Adapter struct {
	cfg config.Config

	rest *rest.Client
	ws   *websocket.Client

	apiKey    string
	apiSecret string

	paperVerified atomic.Bool
	started       atomic.Bool
	closed        atomic.Bool
	lastEventNS   atomic.Int64
	lastCID       atomic.Int64

	restMu sync.Mutex
	bookMu sync.RWMutex
	bookAt map[string]time.Time

	accountMu   sync.RWMutex
	lastAccount domain.AccountSnapshot
}

func New(cfg config.Config) *Adapter {
	key := strings.TrimSpace(os.Getenv(cfg.Bitfinex.APIKeyEnv))
	secret := strings.TrimSpace(os.Getenv(cfg.Bitfinex.APISecretEnv))
	client := rest.NewClient()
	if key != "" && secret != "" {
		client.Credentials(key, secret)
	}
	params := websocket.NewDefaultParameters()
	params.ManageOrderbook = true
	params.AutoReconnect = true
	params.ResubscribeOnReconnect = true
	params.ReconnectInterval = cfg.Bitfinex.ReconnectEvery.Duration
	params.ReconnectAttempts = cfg.Bitfinex.ReconnectTries
	params.HeartbeatTimeout = cfg.Bitfinex.HeartbeatTimeout.Duration
	ws := websocket.NewWithParams(params)
	if key != "" && secret != "" {
		ws.Credentials(key, secret).CancelOnDisconnect(true)
	}
	return &Adapter{cfg: cfg, rest: client, ws: ws, apiKey: key, apiSecret: secret, bookAt: make(map[string]time.Time)}
}

func (a *Adapter) Start(ctx context.Context) error {
	if !a.started.CompareAndSwap(false, true) {
		return errors.New("Bitfinex adapter already started")
	}
	if (a.apiKey == "") != (a.apiSecret == "") {
		return errors.New("both Bitfinex API key and secret must be configured together")
	}
	if a.hasCredentials() {
		if err := a.verifyPaperAccount(); err != nil {
			return err
		}
	}
	if err := a.ws.Connect(); err != nil {
		return fmt.Errorf("connect Bitfinex websocket: %w", err)
	}
	for _, symbol := range a.cfg.Symbols.All() {
		subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := a.ws.SubscribeBook(subCtx, symbol, common.Precision0, common.FrequencyRealtime, common.PriceLevelDefault)
		cancel()
		if err != nil {
			a.ws.Close()
			return fmt.Errorf("subscribe book %s: %w", symbol, err)
		}
	}
	go a.listen(ctx)
	return nil
}

func (a *Adapter) Close() error {
	if a.closed.CompareAndSwap(false, true) {
		a.ws.Close()
	}
	return nil
}

func (a *Adapter) listen(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.ws.Listen():
			if !ok {
				return
			}
			now := time.Now().UTC()
			a.lastEventNS.Store(now.UnixNano())
			switch v := event.(type) {
			case *book.Book:
				a.markBook(v.Symbol, now)
			case book.Book:
				a.markBook(v.Symbol, now)
			case *book.Snapshot:
				for _, level := range v.Snapshot {
					if level != nil && level.Symbol != "" {
						a.markBook(level.Symbol, now)
						break
					}
				}
			case book.Snapshot:
				for _, level := range v.Snapshot {
					if level != nil && level.Symbol != "" {
						a.markBook(level.Symbol, now)
						break
					}
				}
			}
		}
	}
}

func (a *Adapter) markBook(symbol string, at time.Time) {
	if symbol == "" {
		return
	}
	a.bookMu.Lock()
	a.bookAt[symbol] = at
	a.bookMu.Unlock()
}

func (a *Adapter) Books(_ context.Context) (map[string]domain.BookSnapshot, error) {
	result := make(map[string]domain.BookSnapshot, len(a.cfg.Symbols.All()))
	var errs []string
	for _, symbol := range a.cfg.Symbols.All() {
		ob, err := a.ws.GetOrderbook(symbol)
		if err != nil {
			errs = append(errs, symbol+": "+err.Error())
			continue
		}
		bids, asks := ob.Bids(), ob.Asks()
		if len(bids) == 0 || len(asks) == 0 {
			errs = append(errs, symbol+": book has no bid or ask")
			continue
		}
		at := a.bookTime(symbol)
		if at.IsZero() {
			at = a.LastEvent()
		}
		result[symbol] = buildBookSnapshot(symbol, bids, asks, a.cfg.Market.BookDepthBPS, at)
	}
	if len(result) == 0 && len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return result, nil
}

func (a *Adapter) Account(_ context.Context, fairPrice float64) (domain.AccountSnapshot, error) {
	if !a.hasCredentials() {
		return domain.AccountSnapshot{}, exchange.ErrNoCredentials
	}
	a.restMu.Lock()
	defer a.restMu.Unlock()

	wallets, err := a.rest.Wallet.Wallet()
	if err != nil {
		return domain.AccountSnapshot{}, fmt.Errorf("retrieve wallets: %w", err)
	}
	positions, posErr := a.rest.Positions.All()
	if posErr != nil && !isEmptySnapshotError(posErr) {
		return domain.AccountSnapshot{}, fmt.Errorf("retrieve positions: %w", posErr)
	}
	orders, ordErr := a.rest.Orders.All()
	if ordErr != nil && !isEmptySnapshotError(ordErr) {
		return domain.AccountSnapshot{}, fmt.Errorf("retrieve active orders: %w", ordErr)
	}

	snapshot := domain.AccountSnapshot{Paper: a.paperVerified.Load(), UpdatedAt: time.Now().UTC()}
	for _, w := range wallets.Snapshot {
		if w == nil {
			continue
		}
		currency := normalizeCurrency(w.Currency)
		balance := w.Balance
		switch currency {
		case "USD", "TESTUSD":
			snapshot.QuoteUSD += balance
			snapshot.FundingCostUSD += math.Abs(w.UnsettledInterest)
		case "UST", "USDT", "TESTUST", "TESTUSDT":
			snapshot.QuoteUSD += balance * a.cfg.Bitfinex.USTHaircut
			snapshot.FundingCostUSD += math.Abs(w.UnsettledInterest) * a.cfg.Bitfinex.USTHaircut
		case "XAUT", "TESTXAUT":
			if strings.EqualFold(w.Type, "exchange") {
				snapshot.SpotXAUT += balance
			}
			snapshot.FundingCostUSD += math.Abs(w.UnsettledInterest) * fairPrice
		}
	}
	var marginBaseWeighted, marginBaseQty float64
	if positions != nil {
		for _, pos := range positions.Snapshot {
			if pos == nil || normalizeSymbol(pos.Symbol) != normalizeSymbol(a.cfg.Symbols.OrderPair) {
				continue
			}
			snapshot.MarginXAUT += pos.Amount
			snapshot.MarginPnLUSD += pos.ProfitLoss
			if pos.BasePrice > 0 {
				q := math.Abs(pos.Amount)
				marginBaseWeighted += pos.BasePrice * q
				marginBaseQty += q
			}
			if pos.LiquidationPrice > 0 {
				snapshot.LiquidationPrice = pos.LiquidationPrice
			}
		}
	}
	if marginBaseQty > 0 {
		snapshot.MarginBasePrice = marginBaseWeighted / marginBaseQty
	}
	if orders != nil {
		for _, o := range orders.Snapshot {
			if o == nil || normalizeSymbol(o.Symbol) != normalizeSymbol(a.cfg.Symbols.OrderPair) {
				continue
			}
			venue := domain.VenueMargin
			if strings.HasPrefix(strings.ToUpper(o.Type), "EXCHANGE ") {
				venue = domain.VenueSpot
			}
			snapshot.OpenOrders = append(snapshot.OpenOrders, domain.OpenOrder{
				ID: o.ID, GID: o.GID, CID: o.CID, Venue: venue, Symbol: o.Symbol, Type: o.Type,
				RemainingAmount: o.Amount, Price: o.Price, Status: o.Status,
				CreatedAt: time.UnixMilli(o.MTSCreated).UTC(),
			})
		}
	}
	snapshot.EquityUSD = snapshot.QuoteUSD + snapshot.SpotXAUT*fairPrice + snapshot.MarginPnLUSD - snapshot.FundingCostUSD
	a.accountMu.Lock()
	a.lastAccount = snapshot
	a.accountMu.Unlock()
	return snapshot, nil
}

func (a *Adapter) Submit(_ context.Context, intent domain.OrderIntent) (string, error) {
	if err := a.orderGuard(); err != nil {
		return "", err
	}
	if normalizeSymbol(intent.Symbol) != normalizeSymbol(a.cfg.Symbols.OrderPair) {
		return "", fmt.Errorf("refusing unexpected symbol %q", intent.Symbol)
	}
	if intent.Quantity <= 0 || intent.LimitPrice <= 0 {
		return "", errors.New("order quantity and price must be positive")
	}
	if err := a.validateIntentAgainstCachedAccount(intent); err != nil {
		return "", err
	}

	cid := intent.CID
	if cid == 0 {
		cid = a.nextCID()
	}
	orderType := strings.ToUpper(strings.TrimSpace(intent.OrderType))
	if orderType == "" {
		orderType = "LIMIT"
	}
	if intent.Venue == domain.VenueSpot {
		orderType = "EXCHANGE " + strings.TrimPrefix(orderType, "EXCHANGE ")
	}
	req := &order.NewRequest{
		GID: intent.GID, CID: cid, Type: orderType, Symbol: intent.Symbol,
		Amount: intent.Amount, Price: intent.LimitPrice, PostOnly: intent.PostOnly, Close: intent.CloseOnly,
		Meta: map[string]interface{}{"protect_selfmatch": 1},
	}
	a.restMu.Lock()
	n, err := a.rest.Orders.SubmitOrder(req)
	a.restMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("submit paper order: %w", err)
	}
	if n == nil {
		return "", errors.New("Bitfinex returned a nil order notification")
	}
	result, _ := json.Marshal(map[string]any{"status": n.Status, "text": n.Text, "message_id": n.MessageID, "cid": cid})
	if !strings.EqualFold(n.Status, "SUCCESS") {
		return string(result), fmt.Errorf("Bitfinex order rejected: %s: %s", n.Status, n.Text)
	}
	return string(result), nil
}

func (a *Adapter) Cancel(_ context.Context, orderID int64) error {
	if err := a.cancelGuard(); err != nil {
		return err
	}
	if orderID <= 0 {
		return errors.New("order ID must be positive")
	}
	a.restMu.Lock()
	defer a.restMu.Unlock()
	return a.rest.Orders.SubmitCancelOrder(&order.CancelRequest{ID: orderID})
}

func (a *Adapter) CancelBotOrders(_ context.Context) error {
	if err := a.cancelGuard(); err != nil {
		return err
	}
	a.restMu.Lock()
	defer a.restMu.Unlock()
	orders, err := a.rest.Orders.All()
	if err != nil {
		if isEmptySnapshotError(err) {
			return nil
		}
		return err
	}
	for _, o := range orders.Snapshot {
		if o == nil || (o.GID != a.cfg.Execution.GroupID && o.GID != a.cfg.Execution.StopGroupID) {
			continue
		}
		if err := a.rest.Orders.SubmitCancelOrder(&order.CancelRequest{ID: o.ID}); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) PaperVerified() bool { return a.paperVerified.Load() }
func (a *Adapter) OrdersEnabled() bool {
	return PaperOnlyBuild && a.paperVerified.Load() && !a.cfg.App.ObserveOnly && os.Getenv(a.cfg.Bitfinex.PaperAckEnv) == a.cfg.Bitfinex.PaperAckValue
}
func (a *Adapter) LastEvent() time.Time {
	n := a.lastEventNS.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

func (a *Adapter) verifyPaperAccount() error {
	if !PaperOnlyBuild {
		return errors.New("invalid build: paper-only invariant disabled")
	}
	a.restMu.Lock()
	defer a.restMu.Unlock()
	req, err := a.rest.NewAuthenticatedRequest(common.PermissionRead, "info/user")
	if err != nil {
		return fmt.Errorf("build user-info request: %w", err)
	}
	raw, err := a.rest.Request(req)
	if err != nil {
		return fmt.Errorf("verify paper account: %w", err)
	}
	if len(raw) <= 21 {
		return fmt.Errorf("user-info response too short to verify PPT_ENABLED: %d fields", len(raw))
	}
	flag, ok := numeric(raw[21])
	if !ok || flag != 1 {
		return errors.New("refusing to start authenticated trading: API key is not attached to a Bitfinex paper-trading account")
	}
	a.paperVerified.Store(true)
	return nil
}

func (a *Adapter) orderGuard() error {
	if !PaperOnlyBuild {
		return errors.New("refusing order: build is not paper-only")
	}
	if !a.paperVerified.Load() {
		return errors.New("refusing order: Bitfinex PPT_ENABLED was not verified")
	}
	if a.cfg.App.ObserveOnly {
		return errors.New("refusing order: observe_only is enabled")
	}
	if os.Getenv(a.cfg.Bitfinex.PaperAckEnv) != a.cfg.Bitfinex.PaperAckValue {
		return fmt.Errorf("refusing order: set %s to the exact paper-only acknowledgement", a.cfg.Bitfinex.PaperAckEnv)
	}
	return nil
}

func (a *Adapter) cancelGuard() error {
	if !PaperOnlyBuild {
		return errors.New("refusing cancellation: build is not paper-only")
	}
	if !a.paperVerified.Load() {
		return errors.New("refusing cancellation: Bitfinex PPT_ENABLED was not verified")
	}
	return nil
}

func (a *Adapter) validateIntentAgainstCachedAccount(in domain.OrderIntent) error {
	a.accountMu.RLock()
	account := a.lastAccount
	a.accountMu.RUnlock()
	if account.UpdatedAt.IsZero() || time.Since(account.UpdatedAt) > a.cfg.Risk.AccountMaximumAge.Duration {
		return errors.New("refusing order: cached account snapshot is stale")
	}
	qty := math.Abs(in.Amount)
	switch {
	case in.Venue == domain.VenueSpot && in.Side == domain.SideSell:
		if qty > account.SpotXAUT+a.cfg.Execution.QuantityStep {
			return fmt.Errorf("refusing spot sell %.6f above held spot %.6f", qty, account.SpotXAUT)
		}
	case in.Venue == domain.VenueMargin && in.Side == domain.SideBuy:
		if !in.CloseOnly {
			return errors.New("refusing margin buy: this bot does not open margin longs")
		}
		shortQty := math.Abs(math.Min(0, account.MarginXAUT))
		if qty > shortQty+a.cfg.Execution.QuantityStep {
			return fmt.Errorf("refusing margin close %.6f above short %.6f", qty, shortQty)
		}
	case in.Venue == domain.VenueMargin && in.Side == domain.SideSell:
		if in.CloseOnly {
			return errors.New("refusing invalid close-only margin sell")
		}
		if account.SpotXAUT > a.cfg.Execution.TargetToleranceXAUT {
			return errors.New("refusing margin short while spot XAUT remains")
		}
	case in.Venue == domain.VenueSpot && in.Side == domain.SideBuy:
		if account.MarginXAUT < -a.cfg.Execution.TargetToleranceXAUT {
			return errors.New("refusing spot long while margin short remains")
		}
	default:
		return errors.New("unsupported order venue/side combination")
	}
	equity := account.EquityUSD
	if equity <= 0 || equity > a.cfg.Risk.CapitalBaseUSD {
		equity = a.cfg.Risk.CapitalBaseUSD
	}
	currentGross := (math.Abs(account.SpotXAUT) + math.Abs(account.MarginXAUT)) * in.LimitPrice
	pendingOpening := 0.0
	for _, o := range account.OpenOrders {
		if o.ID <= 0 {
			continue
		}
		isOpening := (o.Venue == domain.VenueSpot && o.RemainingAmount > 0) || (o.Venue == domain.VenueMargin && o.RemainingAmount < 0)
		if isOpening {
			pendingOpening += math.Abs(o.RemainingAmount) * in.LimitPrice
		}
	}
	opening := (in.Venue == domain.VenueSpot && in.Side == domain.SideBuy) || (in.Venue == domain.VenueMargin && in.Side == domain.SideSell)
	if opening && currentGross+pendingOpening+qty*in.LimitPrice > equity*a.cfg.Risk.AbsoluteGrossExposure+1 {
		return errors.New("refusing order: adapter-level 1x gross exposure cap exceeded")
	}
	return nil
}

func (a *Adapter) PublicTrades(_ context.Context, symbol string, since time.Time, limit int) ([]domain.PublicTrade, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("sort", "1")
	if !since.IsZero() {
		q.Set("start", strconv.FormatInt(since.UnixMilli(), 10))
	}
	raw, err := a.publicRequest("trades/"+symbol+"/hist", q)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PublicTrade, 0, len(raw))
	for _, item := range raw {
		row, ok := item.([]interface{})
		if !ok || len(row) < 4 {
			continue
		}
		id, ok1 := int64num(row[0])
		mts, ok2 := int64num(row[1])
		amount, ok3 := numeric(row[2])
		price, ok4 := numeric(row[3])
		if !ok1 || !ok2 || !ok3 || !ok4 || price <= 0 {
			continue
		}
		out = append(out, domain.PublicTrade{ID: id, Time: time.UnixMilli(mts).UTC(), Amount: amount, Price: price})
	}
	return out, nil
}

func (a *Adapter) Candles(_ context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("sort", "1")
	raw, err := a.publicRequest("candles/trade:"+timeframe+":"+symbol+"/hist", q)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Candle, 0, len(raw))
	for _, item := range raw {
		row, ok := item.([]interface{})
		if !ok || len(row) < 6 {
			continue
		}
		mts, ok0 := int64num(row[0])
		o, ok1 := numeric(row[1])
		c, ok2 := numeric(row[2])
		h, ok3 := numeric(row[3])
		l, ok4 := numeric(row[4])
		v, ok5 := numeric(row[5])
		if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
			continue
		}
		out = append(out, domain.Candle{Time: time.UnixMilli(mts).UTC(), Open: o, Close: c, High: h, Low: l, Volume: v})
	}
	return out, nil
}

func (a *Adapter) Funding(_ context.Context) (domain.FundingSnapshot, error) {
	lookback := a.cfg.Funding.Lookback.Duration
	if lookback <= 0 {
		lookback = 6 * time.Hour
	}
	q := url.Values{}
	q.Set("limit", "1000")
	q.Set("sort", "1")
	q.Set("start", strconv.FormatInt(time.Now().UTC().Add(-lookback).UnixMilli(), 10))
	raw, err := a.publicRequest("trades/"+a.cfg.Symbols.XAUTFunding+"/hist", q)
	if err != nil {
		return domain.FundingSnapshot{}, err
	}
	var weighted, weight float64
	var newest time.Time
	for _, item := range raw {
		row, ok := item.([]interface{})
		if !ok || len(row) < 5 {
			continue
		}
		mts, ok0 := int64num(row[1])
		amount, ok1 := numeric(row[2])
		rate, ok2 := numeric(row[3])
		if !ok0 || !ok1 || !ok2 {
			continue
		}
		w := math.Abs(amount)
		if w == 0 {
			w = 1
		}
		weighted += rate * w
		weight += w
		t := time.UnixMilli(mts).UTC()
		if t.After(newest) {
			newest = t
		}
	}
	if weight == 0 {
		return domain.FundingSnapshot{Symbol: a.cfg.Symbols.XAUTFunding, Valid: false, UpdatedAt: time.Now().UTC(), Reason: "no recent XAUT funding trades"}, errors.New("no recent XAUT funding trades")
	}
	return domain.FundingSnapshot{Symbol: a.cfg.Symbols.XAUTFunding, DailyRate: weighted / weight, Source: "Bitfinex public funding trades", Valid: true, UpdatedAt: newest}, nil
}

func (a *Adapter) publicRequest(ref string, params url.Values) ([]interface{}, error) {
	req := rest.NewRequestWithMethod(ref, "GET")
	req.Params = params
	a.restMu.Lock()
	raw, err := a.rest.Request(req)
	a.restMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("Bitfinex public request %s: %w", ref, err)
	}
	return raw, nil
}

func (a *Adapter) Fills(_ context.Context, since time.Time) ([]domain.Fill, error) {
	if !a.hasCredentials() {
		return nil, exchange.ErrNoCredentials
	}
	data := map[string]interface{}{"limit": 2500, "sort": 1}
	if !since.IsZero() {
		data["start"] = since.UnixMilli()
	}
	a.restMu.Lock()
	req, err := a.rest.NewAuthenticatedRequestWithData(common.PermissionRead, "trades/"+a.cfg.Symbols.OrderPair+"/hist", data)
	if err != nil {
		a.restMu.Unlock()
		return nil, fmt.Errorf("build account trades request: %w", err)
	}
	raw, err := a.rest.Request(req)
	a.restMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("retrieve account trades: %w", err)
	}
	rows := raw
	out := make([]domain.Fill, 0, len(rows))
	for _, item := range rows {
		row, ok := item.([]interface{})
		if !ok || len(row) < 12 {
			continue
		}
		id, ok0 := int64num(row[0])
		mts, ok1 := int64num(row[2])
		oid, ok2 := int64num(row[3])
		amt, ok3 := numeric(row[4])
		px, ok4 := numeric(row[5])
		fee, ok5 := numeric(row[9])
		cid, _ := int64num(row[11])
		if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
			continue
		}
		typ, _ := row[6].(string)
		symbol, _ := row[1].(string)
		feeCcy, _ := row[10].(string)
		out = append(out, domain.Fill{ID: id, OrderID: oid, CID: cid, Symbol: symbol, Amount: amt, Price: px, OrderType: typ, Fee: fee, FeeCurrency: feeCcy, Time: time.UnixMilli(mts).UTC()})
	}
	return out, nil
}

func int64num(v any) (int64, bool) {
	f, ok := numeric(v)
	if !ok {
		return 0, false
	}
	return int64(f), true
}

func (a *Adapter) nextCID() int64 {
	for {
		now := time.Now().UTC().UnixMilli()
		last := a.lastCID.Load()
		if now <= last {
			now = last + 1
		}
		if a.lastCID.CompareAndSwap(last, now) {
			return now
		}
	}
}
func (a *Adapter) hasCredentials() bool { return a.apiKey != "" && a.apiSecret != "" }
func (a *Adapter) bookTime(symbol string) time.Time {
	a.bookMu.RLock()
	defer a.bookMu.RUnlock()
	return a.bookAt[symbol]
}

func buildBookSnapshot(symbol string, bids, asks []book.Book, depthBPS float64, at time.Time) domain.BookSnapshot {
	bestBid, bestAsk := bids[0].Price, asks[0].Price
	bidLimit := bestBid * (1 - depthBPS/10_000)
	askLimit := bestAsk * (1 + depthBPS/10_000)
	var bidBase, bidQuote, askBase, askQuote float64
	for _, level := range bids {
		if level.Price < bidLimit {
			break
		}
		qty := math.Abs(level.Amount)
		bidBase += qty
		bidQuote += qty * level.Price
	}
	for _, level := range asks {
		if level.Price > askLimit {
			break
		}
		qty := math.Abs(level.Amount)
		askBase += qty
		askQuote += qty * level.Price
	}
	snap := domain.BookSnapshot{Symbol: symbol, Bid: bestBid, Ask: bestAsk, BidQty: math.Abs(bids[0].Amount), AskQty: math.Abs(asks[0].Amount), DepthBase: math.Min(bidBase, askBase), DepthQuote: math.Min(bidQuote, askQuote), UpdatedAt: at}
	for i, level := range bids {
		if i >= 10 {
			break
		}
		snap.Bids = append(snap.Bids, domain.BookLevel{Price: level.Price, Quantity: math.Abs(level.Amount)})
	}
	for i, level := range asks {
		if i >= 10 {
			break
		}
		snap.Asks = append(snap.Asks, domain.BookLevel{Price: level.Price, Quantity: math.Abs(level.Amount)})
	}
	return snap
}
func normalizeCurrency(v string) string {
	return strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(v), "t"))
}
func normalizeSymbol(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "T")
	return strings.NewReplacer(":", "", "-", "", "/", "").Replace(v)
}
func isEmptySnapshotError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "data slice too short") || strings.Contains(s, "not an order snapshot") || strings.Contains(s, "not a position snapshot")
}
func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	default:
		return 0, false
	}
}
