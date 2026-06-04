const BASE = import.meta.env.VITE_API_BASE ?? "http://127.0.0.1:38180";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { "Content-Type": "application/json", ...init?.headers },
    ...init,
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status} ${body}`);
  }
  return res.json() as Promise<T>;
}

// ── Types ──────────────────────────────────────────────────────────────────

export interface WatchlistGroup {
  id: number;
  name: string;
}

export interface WatchlistItem {
  id: number;
  group_id: number;
  symbol: string;
  conid: number;
  name: string;
  sec_type: string;
  exchange: string;
  currency: string;
  tags: string;
  notes: string;
}

export interface Quote {
  symbol: string;
  last: number;
  change: number;
  change_pct: number;
  bid: number;
  ask: number;
  volume: number;
  high: number;
  low: number;
}

export interface Position {
  symbol: string;
  conid: number;
  quantity: number;
  avg_cost: number;
  market_price: number;
  market_value: number;
  unrealized_pnl: number;
  realized_pnl: number;
  currency: string;
  account: string;
}

export interface EquityPoint {
  time: string;
  value: number;
  currency: string;
  source: string;
}

export interface AccountSummary {
  net_liquidation: number;
  unrealized_pnl: number;
  realized_pnl: number;
  total_cash_value: number;
  gross_position_value: number;
  buying_power: number;
  broker: string;
}

export interface EquityResponse {
  points: EquityPoint[];
  summary: AccountSummary;
  warning?: string;
}

export interface IBKRStatus {
  running: boolean;
  authenticated: boolean;
  account: string;
  session_age_seconds: number;
}

export interface Settings {
  [key: string]: unknown;
}

export interface NewsArticle {
  headline: string;
  summary: string;
  url: string;
  source: string;
  datetime: number;
}

export interface Candle {
  time: number;   // Unix seconds
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

export interface HistoryResponse {
  symbol: string;
  conid: number;
  period: string;
  bar: string;
  candles: Candle[];
}

// ── API calls ─────────────────────────────────────────────────────────────

export const api = {
  health: () => request<{ status: string }>("/health"),

  watchlist: {
    groups: () => request<WatchlistGroup[]>("/api/v1/watchlist/groups"),
    items: (groupId: number) =>
      request<WatchlistItem[]>(`/api/v1/watchlist/groups/${groupId}/items`),
    upsert: (groupId: number, item: Omit<WatchlistItem, "id" | "group_id">) =>
      request<WatchlistItem>(`/api/v1/watchlist/groups/${groupId}/items`, {
        method: "POST",
        body: JSON.stringify(item),
      }),
    delete: (groupId: number, symbol: string) =>
      request<void>(`/api/v1/watchlist/groups/${groupId}/items/${symbol}`, {
        method: "DELETE",
      }),
  },

  quotes: {
    byConIds: (conids: number[]) =>
      request<Quote[]>(`/api/v1/quotes?conids=${conids.join(",")}`),
    bySymbol: (symbol: string) =>
      request<Quote>(`/api/v1/quotes/${symbol}`),
    history: (symbol: string, period = "1m", bar = "") =>
      request<HistoryResponse>(
        `/api/v1/quotes/${encodeURIComponent(symbol)}/history?period=${period}${bar ? `&bar=${bar}` : ""}`
      ),
  },

  instruments: {
    search: (q: string) =>
      request<WatchlistItem[]>(`/api/v1/instruments/search?q=${encodeURIComponent(q)}`),
  },

  positions: () => request<Position[]>("/api/v1/positions"),

  equity: () => request<EquityResponse>("/api/v1/account/equity"),

  news: (symbol: string) =>
    request<NewsArticle[]>(`/api/v1/news/${symbol}`),

  ibkr: {
    status: () => request<IBKRStatus>("/api/v1/ibkr/gateway/status"),
    start: () =>
      request<{ status: string }>("/api/v1/ibkr/gateway/start", { method: "POST" }),
    stop: () =>
      request<{ status: string }>("/api/v1/ibkr/gateway/stop", { method: "POST" }),
    reconnect: () =>
      request<{ status: string }>("/api/v1/ibkr/gateway/reconnect", { method: "POST" }),
  },

  settings: {
    get: () => request<Settings>("/api/v1/settings"),
    put: (s: Settings) =>
      request<Settings>("/api/v1/settings", { method: "PUT", body: JSON.stringify(s) }),
    defaults: () => request<Settings>("/api/v1/settings/defaults"),
  },

  server: {
    status: () => request<{ uptime_seconds: number; api_url: string }>("/api/v1/server/status"),
    shutdown: () =>
      request<void>("/api/v1/server/shutdown", { method: "POST" }),
  },
};
