package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	InstrumentStatusActive = "active"
)

// Instrument is Traio's stable identity for one tradable asset. Symbol remains
// the public identifier; ID remains stable across broker mappings and renames.
type Instrument struct {
	ID               int64  `json:"id"`
	AssetType        string `json:"asset_type"`
	Market           string `json:"market"`
	Symbol           string `json:"symbol"`
	NormalizedSymbol string `json:"normalized_symbol"`
	Name             string `json:"name,omitempty"`
	Currency         string `json:"currency,omitempty"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// InstrumentIdentity is the normalized evidence available while synchronizing
// one provider position.
type InstrumentIdentity struct {
	ProviderCode string
	ExternalID   string
	AssetType    string
	Market       string
	Symbol       string
	Name         string
	Exchange     string
	Currency     string
}

func NormalizeInstrumentSymbol(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	symbol = strings.ReplaceAll(symbol, "/", ".")
	return strings.Join(strings.Fields(symbol), ".")
}

func normalizeInstrumentIdentity(identity InstrumentIdentity) (InstrumentIdentity, error) {
	identity.ProviderCode = strings.ToUpper(strings.TrimSpace(identity.ProviderCode))
	identity.ExternalID = strings.TrimSpace(identity.ExternalID)
	identity.Symbol = NormalizeInstrumentSymbol(identity.Symbol)
	identity.Name = strings.TrimSpace(identity.Name)
	identity.Exchange = strings.ToUpper(strings.TrimSpace(identity.Exchange))
	identity.Currency = strings.ToUpper(strings.TrimSpace(identity.Currency))
	identity.AssetType = normalizeInstrumentAssetType(identity.AssetType)
	identity.Market = normalizeInstrumentMarket(identity.Market, identity.Exchange, identity.Currency)
	if identity.Symbol == "" {
		return InstrumentIdentity{}, fmt.Errorf("instrument symbol is required")
	}
	if identity.ProviderCode == "" && identity.ExternalID != "" {
		return InstrumentIdentity{}, fmt.Errorf("provider code is required with external ID")
	}
	return identity, nil
}

func normalizeInstrumentAssetType(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "SECURITY", "EQUITY", "US_EQUITY", "STOCK":
		return "stock"
	case "OPTION", "OPTION_CONTRACT":
		return "option"
	case "ETF":
		return "etf"
	case "MUTUAL_FUND", "MUTUALFUND":
		return "mutual_fund"
	case "FIXED_INCOME", "BOND":
		return "bond"
	case "CRYPTO", "CRYPTOCURRENCY":
		return "crypto"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeInstrumentMarket(market, exchange, currency string) string {
	market = strings.ToUpper(strings.TrimSpace(market))
	if market != "" {
		return market
	}
	switch strings.ToUpper(strings.TrimSpace(exchange)) {
	case "NASDAQ", "NYSE", "NYSEARCA", "ARCA", "AMEX", "BATS", "IEX":
		return "US"
	}
	if strings.EqualFold(strings.TrimSpace(currency), "USD") {
		return "US"
	}
	if currency != "" {
		return strings.ToUpper(strings.TrimSpace(currency))
	}
	return "UNKNOWN"
}

func (s *Store) ResolveInstrument(ctx context.Context, identity InstrumentIdentity) (Instrument, error) {
	identity, err := normalizeInstrumentIdentity(identity)
	if err != nil {
		return Instrument{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Instrument{}, err
	}
	defer tx.Rollback()
	instrument, err := s.resolveInstrumentTx(ctx, tx, identity)
	if err != nil {
		return Instrument{}, err
	}
	if err := tx.Commit(); err != nil {
		return Instrument{}, err
	}
	return instrument, nil
}

func (s *Store) resolveInstrumentTx(ctx context.Context, tx *sql.Tx, identity InstrumentIdentity) (Instrument, error) {
	identity, err := normalizeInstrumentIdentity(identity)
	if err != nil {
		return Instrument{}, err
	}
	if identity.ProviderCode != "" && identity.ExternalID != "" {
		instrument, err := s.getInstrumentByBrokerIdentityTx(ctx, tx, identity.ProviderCode, identity.ExternalID)
		if err == nil {
			if _, err := s.txExecContext(ctx, tx, `
				UPDATE broker_instruments SET broker_symbol = ?, broker_exchange = ?, last_seen_at = ?
				WHERE provider_code = ? AND external_id = ?`,
				identity.Symbol, identity.Exchange, nowRFC3339(), identity.ProviderCode, identity.ExternalID); err != nil {
				return Instrument{}, err
			}
			return instrument, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Instrument{}, err
		}
	}

	if _, err := s.txExecContext(ctx, tx, `
		INSERT INTO instruments (
			asset_type, market, symbol, normalized_symbol, name, currency, status
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_type, market, normalized_symbol) DO UPDATE SET
			name = CASE WHEN instruments.name = '' THEN excluded.name ELSE instruments.name END,
			currency = CASE WHEN instruments.currency = '' THEN excluded.currency ELSE instruments.currency END,
			updated_at = CURRENT_TIMESTAMP`,
		identity.AssetType, identity.Market, identity.Symbol, identity.Symbol,
		identity.Name, identity.Currency, InstrumentStatusActive); err != nil {
		return Instrument{}, err
	}
	instrument, err := s.getInstrumentByCanonicalKeyTx(ctx, tx, identity.AssetType, identity.Market, identity.Symbol)
	if err != nil {
		return Instrument{}, err
	}
	if identity.ProviderCode != "" && identity.ExternalID != "" {
		if _, err := s.txExecContext(ctx, tx, `
			INSERT INTO broker_instruments (
				provider_code, external_id, instrument_id, broker_symbol, broker_exchange, last_seen_at
			) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(provider_code, external_id) DO UPDATE SET
				broker_symbol = excluded.broker_symbol,
				broker_exchange = excluded.broker_exchange,
				last_seen_at = excluded.last_seen_at`,
			identity.ProviderCode, identity.ExternalID, instrument.ID,
			identity.Symbol, identity.Exchange, nowRFC3339()); err != nil {
			return Instrument{}, err
		}
	}
	return instrument, nil
}

func (s *Store) getInstrumentByBrokerIdentityTx(ctx context.Context, tx *sql.Tx, providerCode, externalID string) (Instrument, error) {
	var instrument Instrument
	err := tx.QueryRowContext(ctx, s.bind(`
		SELECT i.id, i.asset_type, i.market, i.symbol, i.normalized_symbol,
			i.name, i.currency, i.status, i.created_at, i.updated_at
		FROM broker_instruments bi
		JOIN instruments i ON i.id = bi.instrument_id
		WHERE bi.provider_code = ? AND bi.external_id = ?`), providerCode, externalID).Scan(
		&instrument.ID, &instrument.AssetType, &instrument.Market, &instrument.Symbol,
		&instrument.NormalizedSymbol, &instrument.Name, &instrument.Currency,
		&instrument.Status, &instrument.CreatedAt, &instrument.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Instrument{}, ErrNotFound
	}
	return instrument, err
}

func (s *Store) getInstrumentByCanonicalKeyTx(ctx context.Context, tx *sql.Tx, assetType, market, symbol string) (Instrument, error) {
	var instrument Instrument
	err := tx.QueryRowContext(ctx, s.bind(`
		SELECT id, asset_type, market, symbol, normalized_symbol, name, currency,
			status, created_at, updated_at
		FROM instruments
		WHERE asset_type = ? AND market = ? AND normalized_symbol = ?`), assetType, market, symbol).Scan(
		&instrument.ID, &instrument.AssetType, &instrument.Market, &instrument.Symbol,
		&instrument.NormalizedSymbol, &instrument.Name, &instrument.Currency,
		&instrument.Status, &instrument.CreatedAt, &instrument.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Instrument{}, ErrNotFound
	}
	return instrument, err
}

func (s *Store) GetInstrument(ctx context.Context, instrumentID int64) (Instrument, error) {
	var instrument Instrument
	err := s.queryRowContext(ctx, `
		SELECT id, asset_type, market, symbol, normalized_symbol, name, currency,
			status, created_at, updated_at
		FROM instruments WHERE id = ?`, instrumentID).Scan(
		&instrument.ID, &instrument.AssetType, &instrument.Market, &instrument.Symbol,
		&instrument.NormalizedSymbol, &instrument.Name, &instrument.Currency,
		&instrument.Status, &instrument.CreatedAt, &instrument.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Instrument{}, ErrNotFound
	}
	return instrument, err
}

func (s *Store) ListInstruments(ctx context.Context) ([]Instrument, error) {
	rows, err := s.queryContext(ctx, `
		SELECT id, asset_type, market, symbol, normalized_symbol, name, currency,
			status, created_at, updated_at
		FROM instruments ORDER BY market, symbol, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Instrument{}
	for rows.Next() {
		var instrument Instrument
		if err := rows.Scan(
			&instrument.ID, &instrument.AssetType, &instrument.Market, &instrument.Symbol,
			&instrument.NormalizedSymbol, &instrument.Name, &instrument.Currency,
			&instrument.Status, &instrument.CreatedAt, &instrument.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, instrument)
	}
	return result, rows.Err()
}
