package position

import (
	"testing"
	"time"
	"xaut-paper-bot/internal/config"
	"xaut-paper-bot/internal/domain"
)

func TestSoftwareBackupStopAndContext(t *testing.T) {
	c := config.Default()
	tr, err := New(c.Risk, c.Execution, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_ = tr.SetEntryContext(domain.RegimeTrend, 1.2, .7)
	_ = tr.Arm(.01)
	acct := domain.AccountSnapshot{SpotXAUT: 1, UpdatedAt: now}
	_, err = tr.Reconcile(now, acct, domain.BookSnapshot{Bid: 4300, Ask: 4301, BidQty: 1, AskQty: 1, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	s := tr.State()
	if s.EntryRegime != domain.RegimeTrend || s.StopPrice <= 0 {
		t.Fatalf("bad state %+v", s)
	}
	ev, _ := tr.Reconcile(now.Add(time.Minute), acct, domain.BookSnapshot{Bid: s.StopPrice - 1, Ask: s.StopPrice, BidQty: 1, AskQty: 1, UpdatedAt: now})
	if !ev.ExitRequired {
		t.Fatal("expected backup stop")
	}
}
