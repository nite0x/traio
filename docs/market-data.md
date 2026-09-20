# Market data policy

Traio routes market data by declared use. The server, not the browser, owns the
selection and fallback order.

| Usage | Selection order |
| --- | --- |
| `historical_chart` | Tiingo, then Yahoo |
| `live_quote` | owning broker connection, then Yahoo |
| `portfolio_valuation` historical days | Tiingo, then Yahoo |
| `portfolio_valuation` current UTC day | owning broker connection, then Yahoo |
| `reconciliation` historical data | Tiingo, then Yahoo |

Broker priority is used only when the request supplies the exact positive
`connection_id`. The policy never substitutes an arbitrary default broker for a
holding. `source=broker` without a connection ID keeps the legacy route for
backward compatibility.

## API

Existing API authentication and permissions still apply:

```text
GET /api/v1/quotes/AAPL?usage=live_quote&connection_id=12
GET /api/v1/quotes/symbols?symbols=AAPL,MSFT&usage=portfolio_valuation&connection_id=12
GET /api/v1/quotes/AAPL/history?period=1y&bar=1d

GET /api/v1/market-data/providers/tiingo
PUT /api/v1/market-data/providers/tiingo
POST /api/v1/market-data/providers/tiingo/test
DELETE /api/v1/market-data/providers/tiingo
```

`source=auto` is the default. `source=broker` and `source=yahoo` select
quote sources explicitly. Tiingo is historical-only, so `source=tiingo` on a
quote request is rejected. A history request defaults to
`usage=historical_chart`; quote requests default to `usage=live_quote`.

Every successful quote includes `source`, `status`, `as_of`, `delayed`,
`currency`, and `adjustment_type`. Historical responses use the same fields in
their response envelope and include stable attribution:

- Tiingo: `Data sourced by Tiingo`;
- Yahoo: `Data sourced by Yahoo Finance`.

Status values are:

- `official`: the selected broker or Tiingo adjusted EOD history;
- `provisional`: Yahoo fallback data;
- `estimated`: Tiingo reference/current quotes.

Tiingo daily charts use split-and-dividend-adjusted OHLCV and report
`adjustment_type=split_dividend_adjusted`. Intraday Tiingo and Yahoo charts
report `unadjusted`. A mixed-source batch is explicit both per quote and through
`X-Market-Data-Source: mixed`.

Errors include a stable `code`: `not_configured`, `authentication_failed`,
`market_data_permission_denied`, `rate_limited`, `upstream_unavailable`,
`security_not_found`, or `invalid_request`.

## Tiingo BYO token

The current configuration is explicitly deployment-scoped
(`"scope":"deployment"`). It is appropriate for a single-user or
single-workspace deployment. It is not a per-user secret store; multi-tenant
production remains blocked until credentials, requests, and caches are truly
tenant-isolated.

`license_mode` describes handling of public market-data responses and currently
only accepts `transient`. It does not enable or describe daily-performance
retention. Derived performance persistence is controlled independently by
`TRAIO_PERFORMANCE_RETENTION_MODE` plus
`TRAIO_MARKET_DATA_PERSISTENCE_LICENSED=true`.

Configure Tiingo with:

```json
{
  "api_token": "user-owned-token",
  "enabled": true
}
```

`base_url` is optional and defaults to `https://api.tiingo.com`. Blank
`api_token` values preserve an already saved token. GET and PUT responses expose
only `token_configured`; they never return the token or its ciphertext.

`POST /test` validates a candidate token, or the saved deployment token when
the body omits `api_token`. It never saves a candidate. Its stable response is:

```json
{"connected":true,"status":"connected","checked_at":"2026-09-20T00:00:00Z"}
```

Failures use `connected=false`, one of `not_configured`, `unauthorized`,
`rate_limited`, or `unavailable`, plus a stable `error_code`.

`DELETE` returns 204 after disabling Tiingo and clearing the deployment token.
There is no Tiingo persistent candle cache or durable request queue to purge.
Persisted derived performance suffixes whose `source_mix` contains Tiingo are
deleted and marked dirty before future recomputation; broker activities, cash,
positions, and independent broker market-data caches are not deleted.

The token is sent in Tiingo's `Authorization: Token …` header, never in a URL.
Before persistence it is encrypted with AES-GCM using a purpose-bound key
derived from Traio's protected runtime API token. Changing that runtime token
requires saving the Tiingo token again.

Tiingo and Yahoo public responses are parsed directly in memory. Their raw
responses and normalized public candles are not written to the broker candle
cache, database, queues, backups, or persistent logs. This rule does not remove
the existing broker `candle_cache`; broker-sourced caching is a separate
capability and remains subject to each broker subscription and license.

## Provider notes

- IBKR current quotes use the requested connection's conid search and snapshot.
- Schwab current quotes use the requested OAuth connection.
- Alpaca current quotes use the Market Data snapshot API; `data_base_url`
  defaults to `https://data.alpaca.markets` and is separate from the trading API.
- Yahoo keeps its short in-memory quote/chart caches and remains available when
  Tiingo is disabled or unavailable.

The server does not implement or assume a data-redistribution license. Tiingo
attribution remains available in every response's `source` field; a client that
displays the data should surface that attribution and comply with the user's
plan.

## Verification

```sh
go test ./...
go vet ./...
git diff --check
```

Tests use local HTTP servers and synthetic credentials. They cover provider
selection and fallback, attribution, Tiingo test/delete and derived-result
invalidation, adjusted chart versus unadjusted valuation history, credential
encryption/non-disclosure, and broker-to-Yahoo current quote routing without
making live market-data requests.
