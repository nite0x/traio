# Schwab Streamer API notes

## Connection and authentication

The Streamer API sends market data and account activity as JSON over
WebSockets.

1. Obtain an OAuth access token.
2. Call Trader API `GET /userPreference`.
3. Read `streamerSocketUrl`, `schwabClientCustomerId`,
   `schwabClientCorrelId`, `schwabClientChannel`, and
   `schwabClientFunctionId`.
4. Connect to `streamerSocketUrl`.
5. Send an `ADMIN` `LOGIN` request.
6. Wait for a successful login response before subscribing.

Only one Streamer connection per user is allowed at a time according to the
supplied documentation.

Login parameters:

- `Authorization`: current OAuth access token
- `SchwabClientChannel`: value from user preference
- `SchwabClientFunctionId`: value from user preference

## Request envelope

```json
{
  "requests": [
    {
      "service": "LEVELONE_EQUITIES",
      "command": "SUBS",
      "requestid": "2",
      "SchwabClientCustomerId": "...",
      "SchwabClientCorrelId": "...",
      "parameters": {
        "keys": "AAPL,SPY",
        "fields": "0,1,2,3,8,10,11,18,42"
      }
    }
  ]
}
```

Commands:

- `LOGIN`: authenticate a new connection.
- `SUBS`: replace all subscriptions for a service.
- `ADD`: add keys without replacing existing subscriptions.
- `UNSUBS`: remove keys.
- `VIEW`: change fields for all keys subscribed to a service.
- `LOGOUT`: log out and close the connection.

Serialize commands. The supplied documentation identifies parallel command
processing as a common cause of command failures.

## Response envelopes

- `response`: result of a command.
- `notify`: heartbeat or other notification.
- `data`: streaming content.

Delivery types:

- `Change`: only changed subscribed fields; data may be conflated.
- `Whole`: complete throttled unit.
- `All Sequence`: every item with sequence information.

Streamer field names are compact numeric strings. Data for `Change` services
must be merged into a per-symbol cache because each message may contain only a
subset of fields.

## Services

| Service | Purpose | Delivery |
| --- | --- | --- |
| `LEVELONE_EQUITIES` | Level 1 equities | Change |
| `LEVELONE_OPTIONS` | Level 1 options | Change |
| `LEVELONE_FUTURES` | Level 1 futures | Change |
| `LEVELONE_FUTURES_OPTIONS` | Level 1 futures options | Change |
| `LEVELONE_FOREX` | Level 1 forex | Change |
| `NYSE_BOOK` | NYSE equity book | Whole |
| `NASDAQ_BOOK` | Nasdaq equity book | Whole |
| `OPTIONS_BOOK` | Options book | Whole |
| `CHART_EQUITY` | Equity candles | All Sequence |
| `CHART_FUTURES` | Futures candles | All Sequence |
| `SCREENER_EQUITY` | Equity advances/decliners | Whole |
| `SCREENER_OPTION` | Option advances/decliners | Whole |
| `ACCT_ACTIVITY` | Account/order activity | All Sequence |

The source documentation inconsistently calls the account service
`ACCOUNT_ACTIVITY` in one table and `ACCT_ACTIVITY` elsewhere. The service list
and request example use `ACCT_ACTIVITY`; verify against production.

## Common response fields

Streamer content may also include:

- `key`: requested symbol or subscription key.
- `delayed`: whether data is NFL/delayed instead of consolidated SIP data.
- `assetMainType`, `assetSubType`, and `cusip`.

### LEVELONE_EQUITIES

| Field | Meaning |
| --- | --- |
| `0` | Symbol |
| `1` | Bid price |
| `2` | Ask price |
| `3` | Last price |
| `4` | Bid size, generally lots |
| `5` | Ask size, generally lots |
| `8` | Total volume |
| `9` | Last size, shares |
| `10` | Day high |
| `11` | Day low |
| `12` | Previous close |
| `17` | Open price |
| `18` | Net change |
| `29` | Regular-market last price |
| `31` | Regular-market net change |
| `32` | Security status |
| `33` | Mark price |
| `34` | Quote time, epoch milliseconds |
| `35` | Trade time, epoch milliseconds |
| `42` | Net percent change |
| `43` | Regular-market percent change |
| `46` | Hard-to-borrow quantity |
| `47` | Hard-to-borrow rate |
| `48` | Hard-to-borrow flag |
| `49` | Shortable flag |
| `50` | Post-market net change |
| `51` | Post-market percent change |

### LEVELONE_OPTIONS

Important fields:

- `0` symbol; `2` bid; `3` ask; `4` last; `8` volume; `9` open interest
- `10` volatility; `20` strike; `21` call/put; `22` underlying
- `28` delta; `29` gamma; `30` theta; `31` vega; `32` rho
- `34` theoretical value; `35` underlying price; `37` mark
- `38` quote time; `39` trade time; `44` net percent change
- `48` penny-pilot flag; `49` option root; `55` exercise type

Standard equity option symbols use a six-character space-filled root followed
by `YYMMDD`, `C` or `P`, and an eight-digit strike.

### LEVELONE_FUTURES

Important fields:

- `0` symbol; `1` bid; `2` ask; `3` last; `8` volume
- `10` quote time; `11` trade time; `12` high; `13` low; `14` close
- `19` net change; `20` percent change; `23` open interest; `24` mark
- `25` tick; `26` tick amount; `27` product; `29` trading hours
- `30` tradable; `31` multiplier; `32` active; `33` settlement price
- `34` active symbol; `35` expiration date

### LEVELONE_FUTURES_OPTIONS

Important fields:

- `0` symbol; `1` bid; `2` ask; `3` last; `8` volume
- `18` open interest; `19` mark; `20` tick; `21` tick amount
- `22` multiplier; `23` settlement price; `24` underlying
- `25` strike; `26` expiration date; `28` contract type

### LEVELONE_FOREX

Important fields:

- `0` symbol; `1` bid; `2` ask; `3` last; `6` volume
- `8` quote time; `9` trade time; `10` high; `11` low; `12` close
- `16` net change; `17` percent change; `20` security status; `29` mark

### CHART_EQUITY

- `0` key, `1` open, `2` high, `3` low, `4` close, `5` volume,
  `6` sequence, `7` epoch-millisecond chart time, `8` chart day.

### CHART_FUTURES

- `0` key, `1` epoch-millisecond chart time, `2` open, `3` high, `4` low,
  `5` close, `6` volume.

### ACCT_ACTIVITY

- `seq`: message sequence, useful for duplicate suppression.
- `key`: client-provided subscription key.
- `1`: account number.
- `2`: message type.
- `3`: JSON-formatted, null, or plain-text message data.

Treat account-activity messages as sensitive and never log their full content.

## Response codes

| Code | Name | Action |
| --- | --- | --- |
| `0` | `SUCCESS` | Continue |
| `3` | `LOGIN_DENIED` | Reconnect and log in with a valid token |
| `9` | `UNKNOWN_FAILURE` | Record correlation ID and investigate |
| `11` | `SERVICE_NOT_AVAILABLE` | Verify service and retry later |
| `12` | `CLOSE_CONNECTION` | Enforce the one-connection limit |
| `19` | `REACHED_SYMBOL_LIMIT` | Reduce subscriptions |
| `20` | `STREAM_CONN_NOT_FOUND` | Wait for login success and preserve IDs |
| `21` | `BAD_COMMAND_FORMAT` | Fix request formatting |
| `22`-`25` | Failed command | Serialize commands and investigate |
| `26`-`29` | Succeeded command | Continue |
| `30` | `STOP_STREAMING` | Reconnect only after addressing inactivity/slowness |

## Implementation guidance

- Wait for successful `LOGIN` before `SUBS`.
- Maintain a single connection per user.
- Merge partial `Change` messages into a per-symbol cache.
- Detect heartbeat/read timeouts and reconnect with bounded backoff.
- Re-fetch user preference and a valid access token when reconnecting.
- Re-subscribe after reconnecting.
- Do not log access tokens, customer IDs, correlation IDs, account numbers, or
  account activity payloads.
