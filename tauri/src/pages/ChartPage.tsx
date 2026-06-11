import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useLiveQuotes } from "../hooks/useLiveQuotes";
import { init, dispose, type Chart } from "klinecharts";
import { ArrowLeft, RefreshCw, BarChart2, TrendingUp } from "lucide-react";
import { api, type Candle, type Position } from "../api/client";
import { fmt } from "../utils/fmt";
import { Spinner, Button, Segmented } from "../components/ui";
import "./ChartPage.css";

// ── Period config ─────────────────────────────────────────────────────────────

interface PeriodConfig {
  label: string;
  period: string;
  bar: string;
}

const PERIODS: PeriodConfig[] = [
  { label: "1D",  period: "1d",  bar: "5min"  },
  { label: "5D",  period: "5d",  bar: "30min" },
  { label: "1M",  period: "1m",  bar: "1h"    },
  { label: "3M",  period: "3m",  bar: "1d"    },
  { label: "6M",  period: "6m",  bar: "1d"    },
  { label: "1Y",  period: "1y",  bar: "1d"    },
  { label: "2Y",  period: "2y",  bar: "1w"    },
  { label: "5Y",  period: "5y",  bar: "1w"    },
];

// ── CSS var helpers ───────────────────────────────────────────────────────────

function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// ── KLineChart pane ───────────────────────────────────────────────────────────

interface ChartPaneProps {
  candles: Candle[];
  position?: Position | null;
}

function ChartPane({ candles, position }: ChartPaneProps) {
  const containerRef  = useRef<HTMLDivElement>(null);
  const chartRef      = useRef<Chart | null>(null);
  // Keep latest candles accessible inside DataLoader callback without re-init
  const candlesRef    = useRef<Candle[]>(candles);
  candlesRef.current  = candles;

  // Init chart once
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const up     = cssVar("--up")      || "#57BD7D";
    const down   = cssVar("--down")    || "#E55B5B";
    const text2  = cssVar("--text-2")  || "#6A6D78";
    const text3  = cssVar("--text-3")  || "#969AA4";
    const border = cssVar("--border")  || "#E1E2E6";
    const accent = cssVar("--accent")  || "#6C5DD3";
    const font   = cssVar("--font-mono") || "JetBrains Mono, monospace";

    const chart = init(el, {
      styles: {
        grid: {
          horizontal: { color: border, style: "dashed", dashedValue: [4, 4] },
          vertical:   { color: border, style: "dashed", dashedValue: [4, 4] },
        },
        candle: {
          bar: {
            upColor:             up,   downColor:             down,   noChangeColor:       text3,
            upBorderColor:       up,   downBorderColor:       down,   noChangeBorderColor: text3,
            upWickColor:         up,   downWickColor:         down,   noChangeWickColor:   text3,
          },
          priceMark: {
            last: {
              upColor: up, downColor: down, noChangeColor: text3,
              line: { show: true, style: "dashed", dashedValue: [4, 2] },
              text: { show: true, size: 11, family: font,
                      paddingLeft: 4, paddingRight: 4, paddingTop: 2, paddingBottom: 2 },
            },
            high: { show: true, color: text2, textOffset: 5, textSize: 10, textFamily: font },
            low:  { show: true, color: text2, textOffset: 5, textSize: 10, textFamily: font },
          },
          tooltip: { showRule: "always", showType: "standard" },
        },
        indicator: { upColor: up, downColor: down, noChangeColor: text3 },
        xAxis: {
          axisLine: { show: true, color: border },
          tickLine: { show: true, color: border },
          tickText: { show: true, color: text3, size: 11, family: font },
        },
        yAxis: {
          axisLine: { show: true, color: border },
          tickLine: { show: true, color: border },
          tickText: { show: true, color: text3, size: 11, family: font },
        },
        crosshair: {
          horizontal: {
            line: { color: accent, style: "dashed", dashedValue: [4, 2] },
            text: { show: true, color: "#fff", size: 11, family: font, backgroundColor: accent },
          },
          vertical: {
            line: { color: accent, style: "dashed", dashedValue: [4, 2] },
            text: { show: true, color: "#fff", size: 11, family: font, backgroundColor: accent },
          },
        },
        overlay: { point: { color: accent } },
      },
      layout: {
        panes: [
          {
            type: "candle",
            options: { id: "candle_pane", height: 400, minHeight: 200 },
            content: ["MA", "EMA"],
          },
          {
            type: "indicator",
            options: { id: "vol_pane", height: 100, minHeight: 60 },
            content: ["VOL"],
          },
          { type: "xAxis" },
        ],
      },
    });

    if (!chart) return;
    chartRef.current = chart;

    // Register DataLoader — getBars is called by KLineChart whenever symbol/period changes.
    // We keep data in candlesRef so the callback always sees the latest candles.
    chart.setDataLoader({
      getBars: ({ callback }) => {
        const data = [...candlesRef.current]
          .sort((a, b) => a.time - b.time)
          .map((c) => ({
            timestamp: c.time * 1000,
            open:      c.open,
            high:      c.high,
            low:       c.low,
            close:     c.close,
            volume:    c.volume,
            turnover:  0,
          }));
        callback(data, { forward: false, backward: false });
      },
    });

    // Trigger initial data load
    chart.setSymbol({ name: "symbol" });
    chart.setPeriod({ type: "day", span: 1 });

    const ro = new ResizeObserver(() => {
      chartRef.current?.resize();
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      dispose(el);
      chartRef.current = null;
    };
  }, []);

  // When candles update (period switch), reload data
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || candles.length === 0) return;
    // resetData clears existing bars and re-triggers getBars
    chart.resetData();
  }, [candles]);

  // Position avg-cost horizontal line
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || !position || position.avg_cost <= 0 || position.quantity === 0) return;

    const accent = cssVar("--accent") || "#6C5DD3";
    const qty    = position.quantity;
    const label  = `持仓均价 ×${qty > 0 ? "+" : ""}${qty}  ${fmt.price(position.avg_cost)}`;

    chart.createOverlay({
      name:   "horizontalRayLine",
      id:     "pos_avg_cost",
      paneId: "candle_pane",
      lock:   true,
      styles: { line: { color: accent, style: "dashed", dashedValue: [6, 3], size: 1 } },
      extendData: label,
      points: [{ value: position.avg_cost }],
    });
  }, [position, candles]);

  return <div ref={containerRef} className="chart-pane" />;
}

// ── TradingView Advanced Chart Widget ─────────────────────────────────────────

function TradingViewPane({ symbol }: { symbol: string }) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    el.innerHTML = "";

    const script = document.createElement("script");
    script.src = "https://s3.tradingview.com/external-embedding/embed-widget-advanced-chart.js";
    script.async = true;
    script.innerHTML = JSON.stringify({
      autosize: true,
      symbol: symbol,
      interval: "D",
      timezone: "America/New_York",
      theme: "light",
      style: "1",           // candlestick
      locale: "zh_CN",
      allow_symbol_change: false,
      calendar: false,
      support_host: "https://www.tradingview.com",
    });

    const wrap = document.createElement("div");
    wrap.className = "tradingview-widget-container__widget";
    wrap.style.height = "100%";
    wrap.style.width = "100%";

    el.appendChild(wrap);
    el.appendChild(script);

    return () => { el.innerHTML = ""; };
  }, [symbol]);

  return (
    <div
      ref={containerRef}
      className="tradingview-widget-container"
      style={{ height: "100%", width: "100%" }}
    />
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function ChartPage() {
  const { symbol } = useParams<{ symbol: string }>();
  useLiveQuotes(symbol ? [symbol] : []);
  const navigate = useNavigate();
  const [mode, setMode] = useState<"ibkr" | "tv">("ibkr");
  const [periodIdx, setPeriodIdx] = useState(2); // default 1M
  const { period, bar } = PERIODS[periodIdx];

  const {
    data: history,
    isLoading,
    isError,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ["history", symbol, period, bar],
    queryFn: () => api.quotes.history(symbol!, period, bar),
    enabled: !!symbol,
    staleTime: 60_000,
  });

  const { data: quote } = useQuery({
    queryKey: ["quote-symbol", symbol],
    queryFn: () => api.quotes.bySymbol(symbol!),
    enabled: !!symbol,
    refetchInterval: 30_000,
  });

  const { data: positions = [] } = useQuery({
    queryKey: ["positions"],
    queryFn: api.positions,
    refetchInterval: 30_000,
  });

  const candles = history?.candles ?? [];
  const pctUp = (quote?.change_pct ?? 0) >= 0;
  const position = positions.find(
    (p) => p.symbol.toUpperCase() === symbol?.toUpperCase()
  ) ?? null;

  const periodItems = PERIODS.map((p, i) => ({ value: String(i), label: p.label }));

  return (
    <div className="page chart-page">
      {/* Header */}
      <div className="page-header">
        <div className="page-header__left">
          <Button
            variant="ghost"
            size="sm"
            icon={<ArrowLeft size={15} />}
            onClick={() => navigate(-1)}
          />
          <div className="chart-header-symbol">
            <span className="chart-header-symbol__code">{symbol}</span>
            {quote && (
              <>
                <span className="chart-header-symbol__price mono">
                  {fmt.price(quote.last)}
                </span>
                <span className={`chart-header-symbol__change mono ${pctUp ? "up" : "down"}`}>
                  {pctUp ? "+" : ""}{fmt.pct(quote.change_pct)}
                </span>
              </>
            )}
          </div>
        </div>
        <div className="page-header__right chart-header-right">
          {mode === "ibkr" && (
            <>
              <Segmented
                items={periodItems}
                value={String(periodIdx)}
                onChange={(v) => setPeriodIdx(Number(v))}
              />
              <Button
                variant="ghost"
                size="sm"
                icon={<RefreshCw size={14} className={isFetching ? "spin" : ""} />}
                onClick={() => refetch()}
                title="刷新"
              />
            </>
          )}
          <div className="chart-mode-tabs">
            <button
              className={`chart-mode-tab${mode === "ibkr" ? " chart-mode-tab--active" : ""}`}
              onClick={() => setMode("ibkr")}
              title="IBKR 数据"
            >
              <BarChart2 size={13} />
              IBKR
            </button>
            <button
              className={`chart-mode-tab${mode === "tv" ? " chart-mode-tab--active" : ""}`}
              onClick={() => setMode("tv")}
              title="TradingView"
            >
              <TrendingUp size={13} />
              TV
            </button>
          </div>
        </div>
      </div>

      {/* Chart area */}
      <div className="chart-body">
        {mode === "tv" ? (
          <TradingViewPane key={symbol} symbol={symbol!} />
        ) : (
          <>
            {(isLoading || isFetching) && candles.length === 0 && <Spinner />}
            {isError && (
              <div className="chart-error">
                <span>获取 K 线数据失败</span>
                <Button variant="default" size="sm" onClick={() => refetch()}>重试</Button>
              </div>
            )}
            {!isLoading && !isError && candles.length === 0 && !isFetching && (
              <div className="chart-empty">暂无数据（需要 IBKR Gateway 连接）</div>
            )}
            {candles.length > 0 && <ChartPane key={`${symbol}-${period}-${bar}`} candles={candles} position={position} />}
            {isFetching && candles.length > 0 && (
              <div className="chart-fetching-overlay">
                <div className="spinner" />
              </div>
            )}
          </>
        )}
      </div>

      {/* Quote stats bar */}
      {quote && (
        <div className="chart-stats-bar">
          <div className="chart-stat">
            <span className="chart-stat__label">开</span>
            <span className="chart-stat__value mono">{fmt.price(quote.bid)}</span>
          </div>
          <div className="chart-stat">
            <span className="chart-stat__label">高</span>
            <span className="chart-stat__value mono up">{fmt.price(quote.high)}</span>
          </div>
          <div className="chart-stat">
            <span className="chart-stat__label">低</span>
            <span className="chart-stat__value mono down">{fmt.price(quote.low)}</span>
          </div>
          <div className="chart-stat">
            <span className="chart-stat__label">量</span>
            <span className="chart-stat__value mono">{fmt.compact(quote.volume)}</span>
          </div>
          <div className="chart-stat">
            <span className="chart-stat__label">买</span>
            <span className="chart-stat__value mono">{fmt.price(quote.bid)}</span>
          </div>
          <div className="chart-stat">
            <span className="chart-stat__label">卖</span>
            <span className="chart-stat__value mono">{fmt.price(quote.ask)}</span>
          </div>
        </div>
      )}
    </div>
  );
}
