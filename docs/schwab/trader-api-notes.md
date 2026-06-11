# Schwab Trader API notes

## Endpoints

OAuth:

- Authorize: `GET https://api.schwabapi.com/v1/oauth/authorize`
- Token exchange and refresh: `POST https://api.schwabapi.com/v1/oauth/token`

Trader API:

- Base URL: `https://api.schwabapi.com/trader/v1`
- Authentication: `Authorization: Bearer {access_token}`

## OAuth flow

Schwab uses the OAuth 2.0 authorization-code flow:

1. Redirect the user to the authorize endpoint with the registered `client_id`
   and exact `redirect_uri`.
2. The user logs into Schwab, grants consent, and selects linked accounts.
3. Schwab redirects to the callback URL with `code` and `session` query
   parameters. The landing page may return 404; the authorization code is still
   present in the URL.
4. URL-decode the authorization code before exchanging it.
5. Exchange it using HTTP Basic authentication with
   `{client_id}:{client_secret}` and form-encoded fields:
   `grant_type=authorization_code`, `code`, and `redirect_uri`.
6. Refresh access with `grant_type=refresh_token` and `refresh_token`.

Operational lifetimes stated by the supplied Schwab documentation:

- Access token: 30 minutes
- Refresh token: 7 days
- After refresh-token expiration or invalidation, repeat user authorization.

Callback URLs must use HTTPS. Schwab documents `https://127.0.0.1` as an
allowed local callback URL. The registered callback URL and token-exchange
`redirect_uri` must match.

## Account identifiers

Call `GET /accounts/accountNumbers` first. It returns plaintext account numbers
paired with encrypted hash values. Use the encrypted `hashValue` as the
`{accountNumber}` path parameter in subsequent account-specific calls.

Do not persist or expose plaintext account numbers unless required by the
application. Account hashes are also sensitive.

## Account and position reads

- `GET /accounts`: balances for all linked accounts.
- `GET /accounts?fields=positions`: balances and positions.
- `GET /accounts/{accountNumber}`: balances for one encrypted account ID.
- `GET /accounts/{accountNumber}?fields=positions`: balances and positions for
  one encrypted account ID.

The account response is polymorphic: `securitiesAccount` is either a cash or
margin account, with different balance objects.

## Orders

Read operations:

- `GET /accounts/{accountNumber}/orders`
- `GET /accounts/{accountNumber}/orders/{orderId}`
- `GET /orders` for all linked accounts

Write operations:

- `POST /accounts/{accountNumber}/orders`
- `PUT /accounts/{accountNumber}/orders/{orderId}`
- `DELETE /accounts/{accountNumber}/orders/{orderId}`
- `POST /accounts/{accountNumber}/previewOrder`

A successful place or replace response has an empty body and returns the new
order URL in the `Location` header.

The supplied documentation states:

- Order entry currently supports equities and options.
- Order `PUT`, `POST`, and `DELETE` calls are throttled per minute per account.
- The configured throttle may range from 0 to 120 requests per minute.
- Order `GET` calls are described as unthrottled.
- Account-specific order search allows a maximum one-year date range.
- The all-account `/orders` description says `fromEnteredTime` must be within
  60 days of the current date.

Supported examples in the supplied documentation include:

- Equity market and limit orders
- Single-leg option limit orders
- Vertical option spreads
- One-triggers-another (`TRIGGER`)
- One-cancels-another (`OCO`)
- Trigger followed by OCO
- Equity trailing stops

Use equity instructions such as `BUY`, `SELL`, `BUY_TO_COVER`, and
`SELL_SHORT`. Use option instructions such as `BUY_TO_OPEN`, `BUY_TO_CLOSE`,
`SELL_TO_OPEN`, and `SELL_TO_CLOSE`.

## Transactions

- `GET /accounts/{accountNumber}/transactions`
- `GET /accounts/{accountNumber}/transactions/{transactionId}`

Transaction-list requirements and constraints:

- `startDate`, `endDate`, and `types` are required.
- Maximum response size is 3,000 transactions.
- Maximum date range is one year.
- Optional `symbol` filtering is supported; URL-encode special characters.

Transaction types:

`TRADE`, `RECEIVE_AND_DELIVER`, `DIVIDEND_OR_INTEREST`, `ACH_RECEIPT`,
`ACH_DISBURSEMENT`, `CASH_RECEIPT`, `CASH_DISBURSEMENT`, `ELECTRONIC_FUND`,
`WIRE_OUT`, `WIRE_IN`, `JOURNAL`, `MEMORANDUM`, `MARGIN_CALL`,
`MONEY_MARKET`, and `SMA_ADJUSTMENT`.

## Known specification issues

Treat real production responses as authoritative and add fixtures when these
areas are implemented:

- The source filename is dated May 11, 2024, while the supplied documentation
  page was published October 30, 2025.
- The source OpenAPI security scheme contained a hard-coded example client ID
  and `scope=readonly`, while top-level security listed `read` and `write`.
  Traio's sanitized specification removes both.
- `GET /accounts/{accountNumber}/transactions/{transactionId}` describes a
  specific transaction but declares an array response.
- `POST /accounts/{accountNumber}/previewOrder` declares `PreviewOrder` as both
  request and response, although normal order fields are nested under
  `orderStrategy`.
- `OrderRequest` includes many response-only fields. Send only fields required
  to express the intended order.
- Several base instrument schemas mark a nonexistent `name` field as required.
- Some schema type declarations are inconsistent, including
  `AccountAPIOptionDeliverable.symbol` as a string with `int64` format.

## Implementation guidance

- Parse structured JSON into explicit internal types; do not depend on raw
  portal descriptions.
- Store the latest refresh token returned by every refresh response. If Schwab
  omits it, retain the previous refresh token.
- Refresh shortly before access-token expiry and serialize concurrent refreshes.
- Log Schwab correlation IDs and HTTP status, but never log tokens, account
  numbers, account hashes, or full response bodies containing account data.
- Validate order intent locally and preview an order before placing it when
  practical.
