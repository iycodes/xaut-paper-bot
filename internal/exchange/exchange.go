package exchange

import (
	"context"
	"errors"
	"time"

	"xaut-paper-bot/internal/domain"
)

var ErrNoCredentials = errors.New("Bitfinex API credentials are not configured")

type Client interface {
	Start(context.Context) error
	Close() error
	Books(context.Context) (map[string]domain.BookSnapshot, error)
	PublicTrades(context.Context, string, time.Time, int) ([]domain.PublicTrade, error)
	Candles(context.Context, string, string, int) ([]domain.Candle, error)
	Funding(context.Context) (domain.FundingSnapshot, error)
	Fills(context.Context, time.Time) ([]domain.Fill, error)
	Account(context.Context, float64) (domain.AccountSnapshot, error)
	Submit(context.Context, domain.OrderIntent) (string, error)
	Cancel(context.Context, int64) error
	CancelBotOrders(context.Context) error
	PaperVerified() bool
	OrdersEnabled() bool
	LastEvent() time.Time
}
