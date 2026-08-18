package store

import (
	"context"
	"testing"
)

func TestResolveInstrumentMapsBrokerIDsToCanonicalAsset(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	identities := []InstrumentIdentity{
		{ProviderCode: "IBKR", ExternalID: "265598", AssetType: "stock", Market: "US", Symbol: "aapl", Name: "Apple Inc.", Currency: "USD"},
		{ProviderCode: "SCHWAB", ExternalID: "037833100", AssetType: "EQUITY", Market: "US", Symbol: "AAPL", Currency: "USD"},
		{ProviderCode: "ALPACA", ExternalID: "b0b6dd9d-8b9b-48a9-ba46-b9d54906e415", AssetType: "us_equity", Market: "US", Symbol: "AAPL", Currency: "USD"},
	}
	var canonicalID int64
	for index, identity := range identities {
		instrument, err := st.ResolveInstrument(ctx, identity)
		if err != nil {
			t.Fatalf("resolve identity %d: %v", index, err)
		}
		if canonicalID == 0 {
			canonicalID = instrument.ID
		}
		if instrument.ID != canonicalID || instrument.Symbol != "AAPL" || instrument.Market != "US" {
			t.Fatalf("identity %d did not resolve to canonical AAPL: %#v", index, instrument)
		}
	}
	var mappings int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM broker_instruments WHERE instrument_id = ?`, canonicalID).Scan(&mappings); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if mappings != 3 {
		t.Fatalf("got %d broker mappings, want 3", mappings)
	}
}

func TestResolveInstrumentKeepsMarketsAndAssetTypesSeparate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	us, err := st.ResolveInstrument(ctx, InstrumentIdentity{AssetType: "stock", Market: "US", Symbol: "BABA", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	hk, err := st.ResolveInstrument(ctx, InstrumentIdentity{AssetType: "stock", Market: "HK", Symbol: "BABA", Currency: "HKD"})
	if err != nil {
		t.Fatal(err)
	}
	option, err := st.ResolveInstrument(ctx, InstrumentIdentity{AssetType: "option", Market: "US", Symbol: "BABA", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if us.ID == hk.ID || us.ID == option.ID || hk.ID == option.ID {
		t.Fatalf("distinct instruments were merged: us=%d hk=%d option=%d", us.ID, hk.ID, option.ID)
	}
}

func TestResolveInstrumentCorrectsBrokerAssetTypeAndName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	incorrect, err := st.ResolveInstrument(ctx, InstrumentIdentity{
		ProviderCode: "IBKR", ExternalID: "320227571", AssetType: "stock", Market: "US", Symbol: "QQQ", Currency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	corrected, err := st.ResolveInstrument(ctx, InstrumentIdentity{
		ProviderCode: "IBKR", ExternalID: "320227571", AssetType: "etf", Market: "US", Symbol: "QQQ", Name: "INVESCO QQQ TRUST SERIES 1", Currency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if corrected.ID == incorrect.ID || corrected.AssetType != "etf" || corrected.Name != "INVESCO QQQ TRUST SERIES 1" {
		t.Fatalf("broker instrument was not corrected: before=%#v after=%#v", incorrect, corrected)
	}
	var mappedID int64
	if err := st.db.QueryRow(`SELECT instrument_id FROM broker_instruments WHERE provider_code = 'IBKR' AND external_id = '320227571'`).Scan(&mappedID); err != nil {
		t.Fatal(err)
	}
	if mappedID != corrected.ID {
		t.Fatalf("mapping points to %d, want %d", mappedID, corrected.ID)
	}
}
