# Schwab Market Data REST API notes

## Overview

- Base URL: `https://api.schwabapi.com/marketdata/v1`
- Authentication: `Authorization: Bearer {access_token}`
- Source specification filename: `TraderApi-MDIS-03-21-2024(4).json`
- Source specification date implied by filename: March 21, 2024

## Endpoint summary

Quotes:

- `GET /quotes`: quote map for a comma-separated symbol list.
- `GET /{symbol_id}/quotes`: quote for one symbol.
- `fields` may select `quote`, `fundamental`, `extended`, `reference`, and
  `regular`. Omit it for the full response.
- `indicative=true` may include indicative ETF symbols such as `$ABC.IV`.
- The multi-quote response is a map keyed by requested symbol.

Options:

- `GET /chains`: contracts grouped by expiration and strike.
- `GET /expirationchain`: available expiration series without contracts.
- Option-chain filters include contract type, strike count, strategy, strike,
  range, dates, expiration month, option type, and entitlement.
- `ANALYTICAL` strategy enables theoretical calculations using volatility,
  underlying price, interest rate, and days to expiration.
- Retail entitlement values are `PN`, `NP`, and `PP`.

Price history:

- `GET /pricehistory`
- Candle timestamps and `startDate`/`endDate` are Unix epoch milliseconds.
- Period types: `day`, `month`, `year`, `ytd`.
- Frequency types: `minute`, `daily`, `weekly`, `monthly`.
- Minute frequencies: `1`, `5`, `10`, `15`, `30`.
- `needExtendedHoursData` and `needPreviousClose` are optional booleans.

Movers:

- `GET /movers/{symbol_id}`
- Supported indexes/groups include `$DJI`, `$COMPX`, `$SPX`, `NYSE`,
  `NASDAQ`, `OTCBB`, `INDEX_ALL`, `EQUITY_ALL`, `OPTION_ALL`, `OPTION_PUT`,
  and `OPTION_CALL`.
- Sort values: `VOLUME`, `TRADES`, `PERCENT_CHANGE_UP`,
  `PERCENT_CHANGE_DOWN`.
- Frequencies: `0`, `1`, `5`, `10`, `30`, `60`.

Market hours:

- `GET /markets`
- `GET /markets/{market_id}`
- Markets: `equity`, `option`, `bond`, `future`, `forex`.
- Optional date format is `YYYY-MM-DD`; the supplied description says the
  range is today through one year in the future.

Instruments:

- `GET /instruments`: search by symbol/description and projection.
- `GET /instruments/{cusip_id}`: lookup by CUSIP.
- Projections: `symbol-search`, `symbol-regex`, `desc-search`, `desc-regex`,
  `search`, and `fundamental`.

## Asset-specific quote responses

Quote payloads are polymorphic by `assetMainType`:

- `EQUITY`
- `OPTION`
- `FOREX`
- `FUTURE`
- `FUTURE_OPTION`
- `INDEX`
- `MUTUAL_FUND`

Common top-level fields include `symbol`, `realtime`, `ssid`, `reference`, and
`quote`. Equities may also include `extended`, `fundamental`, and `regular`.
Option quotes include Greeks and theoretical values.

Do not assume all numeric fields are present or nonzero. The examples contain
zeroes for unavailable fields and sentinel values such as option `vega=-999`.

## Known specification issues

- The source OpenAPI security definition contained a hard-coded example client
  ID and `scope=readonly`, while top-level security listed `read` and `write`.
  The sanitized project specification removes both.
- The source contains internal-looking required header definitions that are
  not attached to the public endpoint operations. Do not send them unless a
  real API response explicitly requires them.
- Quote docs say the combined symbols/CUSIPs/SSIDs limit is 500, but the public
  `/quotes` path only declares `symbols`.
- Some schemas use invalid or inconsistent OpenAPI formats, including
  `format: yyyy-MM-dd`, `format: integer`, `format: long`, numeric timestamps
  declared as both integer and number, and nullable enum entries.
- Some examples are stale and contain expired futures/options.
- Option-chain map schemas do not clearly represent the nested arrays observed
  in common Schwab responses. Treat production fixtures as authoritative.
- `/markets` uses two differently named correlation-header definitions.

## Implementation guidance

- Parse quotes by `assetMainType`, retaining unknown fields when adding a new
  asset type.
- URL-encode symbols containing `$`, `/`, spaces, or option symbology.
- Keep epoch-millisecond values as 64-bit integers.
- Record whether data is realtime or delayed.
- Add fixtures from actual responses before relying on undocumented fields.
- Never log full responses containing entitlement or account-adjacent data.
