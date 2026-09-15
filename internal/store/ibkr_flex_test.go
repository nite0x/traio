package store

import (
	"path/filepath"
	"testing"

	"github.com/nite/traio/internal/broker"
)

func TestIBKRFlexAndGatewayShareAccountAndNeverCombinePositions(t *testing.T) {
	for _, gatewayFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "flex_first", true: "gateway_first"}[gatewayFirst], func(t *testing.T) {
			st := newTestStore(t)
			ctx := t.Context()
			flex, err := st.UpsertBrokerConnection(ctx, BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "flex", AuthType: "api_key", Enabled: true, Config: map[string]any{"connection_type": "flex"}})
			if err != nil {
				t.Fatal(err)
			}
			gateway, err := st.UpsertBrokerConnection(ctx, BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "gateway", AuthType: "gateway", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			accounts := []broker.Account{{ID: "U1", BaseCurrency: "USD"}}
			order := []int64{flex.ID, gateway.ID}
			if gatewayFirst {
				order = []int64{gateway.ID, flex.ID}
			}
			for _, id := range order {
				if err := st.ReplaceBrokerConnectionAccounts(ctx, id, accounts); err != nil {
					t.Fatal(err)
				}
			}
			all, err := st.ListBrokerAccounts(ctx)
			if err != nil || len(all) != 1 || len(all[0].ConnectionIDs) != 2 || *all[0].PrimaryConnectionID != gateway.ID {
				t.Fatalf("accounts=%+v err=%v", all, err)
			}
			pos := func(q float64) []broker.Position {
				return []broker.Position{{Symbol: "AAPL", ConID: 265598, Currency: "USD", Quantity: q, MarketValue: q * 200}}
			}
			if err := st.ReplaceBrokerConnectionAccountPositions(ctx, gateway.ID, "U1", pos(2)); err != nil {
				t.Fatal(err)
			}
			if err := st.ReplaceBrokerConnectionAccountPositions(ctx, flex.ID, "U1", pos(7)); err != nil {
				t.Fatal(err)
			}
			got, err := st.ListBrokerPositions(ctx)
			if err != nil || len(got) != 1 || got[0].Quantity != 2 {
				t.Fatalf("mixed/overwritten=%+v err=%v", got, err)
			}
			instrument, err := st.ResolveInstrument(ctx, InstrumentIdentity{ProviderCode: "IBKR", ExternalID: "265598", AssetType: "stock", Market: "OTHER", Symbol: "AAPL.OTHER", Currency: "USD", PreserveBrokerIdentity: true})
			if err != nil || instrument.ID != got[0].InstrumentID {
				t.Fatalf("Conid association=%+v err=%v", instrument, err)
			}
			if err := st.ReplaceBrokerConnectionAccountPositions(ctx, gateway.ID, "U1", nil); err != nil {
				t.Fatal(err)
			}
			if err := st.ReplaceBrokerConnectionAccountPositions(ctx, flex.ID, "U1", pos(7)); err != nil {
				t.Fatal(err)
			}
			got, err = st.ListBrokerPositions(ctx)
			if err != nil || len(got) != 0 {
				t.Fatalf("Flex resurrected closed positions=%+v err=%v", got, err)
			}
		})
	}
}

func TestLegacyFlexMigrationPreservesSecretsAndAccountLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.UpsertBrokerConnection(t.Context(), BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "gateway", Name: "Gateway", Enabled: true, Config: map[string]any{"gateway_id": "g", "gateway_url": "https://gateway.invalid", "flex_activity_query_id": "123", "flex_query_id": "456"}, Secrets: map[string]string{"gateway_token": "gateway-secret", "flex_token": "flex-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceBrokerConnectionAccounts(t.Context(), old.ID, []broker.Account{{ID: "U1"}}); err != nil {
		t.Fatal(err)
	}
	pending, err := st.CreateHistoryJob(t.Context(), HistoryRequest{ConnectionID: old.ID, Source: "flex", From: "2026-09-01", To: "2026-09-02"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	for i := 0; i < 2; i++ {
		st, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		all, err := st.ListBrokerConnections(t.Context())
		if err != nil || len(all) != 2 {
			t.Fatalf("connections=%+v err=%v", all, err)
		}
		gateway, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), old.ID)
		if err != nil {
			t.Fatal(err)
		}
		if gateway.Secrets["flex_token"] != "" || gateway.Secrets["gateway_token"] != "gateway-secret" || gateway.Config["flex_query_id"] != nil {
			t.Fatal("Gateway migration incorrect")
		}
		for _, c := range all {
			if c.ID != old.ID {
				flex, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), c.ID)
				if err != nil {
					t.Fatal(err)
				}
				if flex.Config["gateway_url"] != nil || flex.Secrets["flex_token"] != "flex-secret" || flex.Config["connection_type"] != "flex" {
					t.Fatal("Flex migration incorrect")
				}
				moved, err := st.GetHistoryJob(t.Context(), pending.ID)
				if err != nil || moved.Request.ConnectionID != flex.ID {
					t.Fatalf("pending job migration=%+v err=%v", moved, err)
				}
				accounts, err := st.ListBrokerAccountsByConnection(t.Context(), flex.ID)
				if err != nil || len(accounts) != 1 {
					t.Fatalf("links=%v err=%v", accounts, err)
				}
			}
		}
		st.Close()
	}
}

func TestIBKRReportSymbolFallbackUsesPositionInstrument(t *testing.T) {
	st := newTestStore(t)
	position, err := st.ResolveInstrument(t.Context(), InstrumentIdentity{ProviderCode: "IBKR", ExternalID: "265598", Symbol: "AAPL", AssetType: "stock", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	trade, err := st.ResolveInstrument(t.Context(), InstrumentIdentity{ProviderCode: "IBKR", Symbol: "AAPL", AssetType: "STK", Currency: "USD", PreserveBrokerIdentity: true})
	if err != nil || trade.ID != position.ID {
		t.Fatalf("symbol match=%+v err=%v", trade, err)
	}
}
