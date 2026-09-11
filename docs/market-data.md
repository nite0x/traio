# Yahoo Finance market data

The server provides Yahoo Finance quotes and historical candles without a broker
login or API key. The Go adapter in `internal/market/yahoo` follows the Yahoo
provider from niteX: cookie warmup, crumb authentication, query1/query2 failover,
bounded retries, and a short quote cache. Cookies and crumbs stay in server memory.

## API

Existing API authentication still applies. Symbol-based endpoints default to Yahoo:

```text
GET /api/v1/quotes/AAPL
GET /api/v1/quotes/symbols?symbols=AAPL,MSFT,0700.HK
GET /api/v1/quotes/AAPL/history?period=1m&bar=1h
```

Use `source=yahoo` to select Yahoo explicitly. Use `source=broker` to retain the
previous IBKR/Schwab routing. Contract-ID quotes (`/api/v1/quotes?conids=...`),
broker positions, account valuation, trading, and the Schwab WebSocket endpoint
remain broker-backed. Yahoo failures return an error, rather than silently
substituting a different data source.

Use Yahoo symbols including exchange suffixes, e.g. `0700.HK`, `2330.TW`,
`600519.SS`, and share-class syntax such as `BRK-B`. No automatic conversion from
broker symbols or contract IDs is performed. A batch accepts up to 100 symbols,
normalizes case, removes duplicates, and preserves request order. Missing or
unpriced symbols are omitted; when all requested symbols are missing the response
is 404. An empty batch returns an empty array.

Quotes include `source`, `currency`, `as_of` (Unix seconds), `market_state`,
`delayed`, and available 52-week high/low values. `last` and daily change describe
the **regular-session** quote; they do not switch to pre/post-market prices. On a
closed market this may be the previous session's price. Do not equate polling
frequency with exchange data freshness. Unknown exchange delay is treated as
delayed.

## History and caching

Periods: `1d`, `5d`, `1m`, `3m`, `6m`, `1y`, `2y`, `5y`.
Bars: `1min`, `5min`, `15min`, `30min`, `1h`, `1d`, `1w`.
Omitting `bar` uses Traio's existing period defaults.

The adapter maps Traio's `1m` period to Yahoo's `1mo`, `1min` bars to `1m`, and
weekly bars to `1wk`. Invalid combinations exceeding the intraday history window
return 400. Missing OHLC samples are skipped rather than converted to zero-price
candles. Timestamps remain Unix seconds.

Quotes are cached for two seconds; charts for thirty seconds. Chart requests with
`refresh=1` bypass the chart cache. The bounded in-memory caches are separate from
broker candle storage. Concurrent requests share the cache and serialized
authentication flow. Network requests time out, use at most three attempts, and
respect cancellation. Upstream 401 triggers credential refresh; 403, 429 and 5xx
use bounded backoff and host failover.
HTTP responses use `Cache-Control: no-store` so browsers and proxies do not add
another stale cache. The provider's in-memory cache remains the source of reuse.

## Frontend

Overview, Holdings and chart quotes poll every thirty seconds. The Yahoo chart also
polls candles every thirty seconds, refreshes on window focus or reconnection, and
keeps existing candles visible during background refresh. Its refresh button
bypasses the server's candle cache with `refresh=1`.

The Overview and Holdings pages fetch quotes through their shared
portfolio hook. Yahoo updates the displayed current price and fills missing
broker daily PnL with `quote.change × current quantity`, marked as an estimate
with the quote source/time. Existing broker daily PnL (including zero), account
totals, position market values and unrealized accounting PnL remain broker data.
Estimates exclude intraday trades and fees. Currency mismatches and derivatives
are not priced from equity ticker quotes. Failed quote requests do not prevent
the broker portfolio from loading.

The legacy Today, Watchlist and Analysis pages have been removed. Their old URLs
fall back to Overview. The chart remains available from portfolio holding rows.

## Verification

```sh
go test -race ./internal/market/... ./internal/api
TRAIO_TEST_YAHOO_LIVE=1 go test ./internal/market/yahoo -run '^TestLiveYahoo$' -count=1 -v
```

The second command makes public network requests for US/Hong Kong quotes and
historical candles. Normal tests use an injected HTTP transport and no Yahoo
network access. Yahoo's public web endpoints can change or rate-limit requests;
their availability is separate from the broker connections' health.
