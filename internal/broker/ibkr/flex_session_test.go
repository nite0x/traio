package ibkr

import (
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/store"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
)

const flexPortfolioFixture = `<FlexQueryResponse><FlexStatements><FlexStatement accountId="U123" fromDate="20260901" toDate="20260902"><AccountInformation currency="USD"/><OpenPositions><OpenPosition conid="265598" symbol="AAPL" assetCategory="STK" currency="USD" position="3" markPrice="200" positionValue="600" costBasisPrice="150" levelOfDetail="SUMMARY"/></OpenPositions><CashReport><CashReportCurrency currency="USD" endingCash="400"/></CashReport><EquitySummaryInBase><EquitySummaryByReportDateInBase reportDate="20260902" total="1000" stock="600"/></EquitySummaryInBase></FlexStatement></FlexStatements></FlexQueryResponse>`

func TestFlexSessionNeedsNoGatewayAndExposesNoTrading(t *testing.T) {
	value, err := NewFactory().Open(t.Context(), broker.ConnectionConfig{ID: 8, Config: map[string]any{"connection_type": "flex", "flex_activity_query_id": "123"}, Secrets: map[string]string{"flex_token": "test-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := value.(broker.TradingProvider); ok {
		t.Fatal("Flex exposes trading")
	}
	calls := 0
	ctx := withFlexTransport(t.Context(), roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Authorization") != "" || req.URL.Host != "ndcdyn.interactivebrokers.com" {
			t.Fatal("unexpected Gateway traffic")
		}
		if calls == 1 {
			return flexHTTPResponse(200, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>REF</ReferenceCode></FlexStatementResponse>`), nil
		}
		return flexHTTPResponse(200, flexPortfolioFixture), nil
	}))
	session := value.(*FlexSession)
	snapshots, err := session.ListAccountSnapshots(ctx)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%v err=%v", snapshots, err)
	}
	s := snapshots[0]
	if s.Account.ID != "U123" || len(s.Positions) != 1 || s.Positions[0].ConID != 265598 || s.Positions[0].Quantity != 3 || s.DailyPerformance.NetLiquidation != 1000 {
		t.Fatalf("snapshot=%+v", s)
	}
	if _, err = session.ListAccountSnapshots(ctx); err != nil || calls != 2 {
		t.Fatalf("cache calls=%d err=%v", calls, err)
	}
}

func TestFlexPortfolioAbsentAndEmptyPositionsAreDifferent(t *testing.T) {
	for _, test := range []struct {
		section string
		missing bool
	}{{"", true}, {"<OpenPositions/>", false}} {
		rows, err := ParseFlexPortfolio([]byte(`<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1" toDate="20260902">` + test.section + `</FlexStatement></FlexStatements></FlexQueryResponse>`))
		if err != nil {
			t.Fatal(err)
		}
		_, errs := rows[0].Resolve(t.Context())
		if (errs.Positions != nil) != test.missing {
			t.Fatalf("missing=%v errors=%v", test.missing, errs)
		}
	}
}

func TestFlexOnlySyncPersistsAccountAndPositionsWithoutNAV(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "flex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "flex", Enabled: true, Config: map[string]any{"connection_type": "flex", "flex_activity_query_id": "123"}, Secrets: map[string]string{"flex_token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewFactory().Open(t.Context(), broker.ConnectionConfig{ID: connection.ID, Config: connection.Config, Secrets: map[string]string{"flex_token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withFlexTransport(t.Context(), roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "SendRequest") {
			return flexHTTPResponse(200, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>REF</ReferenceCode></FlexStatementResponse>`), nil
		}
		return flexHTTPResponse(200, `<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1" toDate="20260902"><AccountInformation currency="USD"/><OpenPositions><OpenPosition symbol="AAPL" currency="USD" position="3" positionValue="600"/></OpenPositions></FlexStatement></FlexStatements></FlexQueryResponse>`), nil
	}))
	svc := portfolio.NewSyncService(st, portfolio.StaticSource("IBKR", connection.ID, session.(*FlexSession)))
	if err = svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	accounts, err := st.ListBrokerAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ProviderAccountID != "U1" {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	positions, err := st.ListBrokerPositions(ctx)
	if err != nil || len(positions) != 1 || positions[0].Quantity != 3 {
		t.Fatalf("positions=%+v err=%v", positions, err)
	}
}
