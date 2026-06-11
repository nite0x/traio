# Schwab Trader API reference

This directory contains the sanitized Schwab Trader API material used by Traio.
It is safe to keep in source control: application credentials, OAuth tokens,
and account data must never be added here.

## Files

- [`trader-api.openapi.yaml`](trader-api.openapi.yaml): project reference
  OpenAPI specification for accounts, orders, transactions, and user
  preferences.
- [`trader-api-notes.md`](trader-api-notes.md): OAuth flow, operational
  constraints, order examples, and known documentation issues.
- [`market-data.openapi.yaml`](market-data.openapi.yaml): sanitized project
  reference for quote, option-chain, price-history, mover, market-hours, and
  instrument endpoints.
- [`market-data-notes.md`](market-data-notes.md): REST market-data behavior,
  parameter constraints, and known documentation issues.
- [`streamer-api-notes.md`](streamer-api-notes.md): WebSocket login,
  subscription, delivery, service, field, and error-code reference.

## Source metadata

- API name: `Retail Trader API Production`
- Supplied specification filename: `TraderApi-Prod_05-11-2024.yaml`
- API base URL: `https://api.schwabapi.com/trader/v1`
- Supplied documentation publication timestamp:
  `2025-10-30T14:39:41+00:00`
- Market Data API name: `Market Data Production`
- Supplied Market Data specification filename:
  `TraderApi-MDIS-03-21-2024(4).json`
- Market Data API base URL: `https://api.schwabapi.com/marketdata/v1`
- Supplied Market Data/Streamer documentation publication timestamp:
  `2024-06-27T14:34:18+00:00`

The OpenAPI files are sanitized project references derived from the supplied
Schwab portal responses. They intentionally exclude portal metadata and the
portal-delivered `appKey`, `appSecret`, and hard-coded OAuth example client
IDs. Those values appear to belong to Schwab's documentation/product metadata,
not to the user's Traio application, and are not needed by this project.

## Secrets

Configure credentials through the local `config.yaml` or environment-specific
secret storage. `config.yaml` and `.env` are ignored by Git.

Never commit credentials or data belonging to the user's Traio application:

- Schwab client ID or client secret
- access, refresh, or ID tokens
- authorization codes
- plaintext account numbers, account hashes, positions, or transactions
