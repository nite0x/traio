import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  createChart,
  CandlestickSeries,
  HistogramSeries,
  ColorType,
  CrosshairMode,
  type IChartApi,
  type ISeriesApi,
  type CandlestickSeriesOptions,
  type HistogramSeriesOptions,
  type Time,
} from "lightweight-charts";
import { ArrowLeft, RefreshCw } from "lucide-react";
import { api, type Candle } from "../api/client";
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

// ── Chart component ───────────────────────────────────────────────────────────

interface ChartPaneProps {
  candles: Candle[];
}

function ChartPane({ candles }: ChartPaneProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef     = useRef<IChartApi | null>(null);
  const candleRef    = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const volRef       = useRef<ISeriesApi<"Histogram"> | null>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const up   = cssVar("--up")   || "#57BD7D";
    const down = cssVar("--down") || "#E55B5B";
    const text3 = cssVar("--text-3") || "#969AA4";
    const border = cssVar("--border") || "#E1E2E6";
    const bg = cssVar("--surface") || "#FFFFFF";

    const chart = createChart(el, {
      layout: {
        background: { type: ColorType.Solid, color: bg },
        textColor: text3,
        fontFamily: cssVar("--font-mono") || "monospace",
        fontSize: 11,
      },
      grid: {
        vertLines: { color: border },
        horzLines: { color: border },
      },
      crosshair: { mode: CrosshairMode.Normal },
      rightPriceScale: { borderColor: border },
      timeScale: {
        borderColor: border,
        timeVisible: true,
        secondsVisible: false,
      },
      width: el.clientWidth,
      height: el.clientHeight,
    });

    // Candlestick series (top 75% of pane)
    const candleSeries = chart.addSeries(CandlestickSeries, {
      upColor:         up,
      downColor:       down,
      borderUpColor:   up,
      borderDownColor: down,
      wickUpColor:     up,
      wickDownColor:   down,
      priceScaleId: "right",
    } as Partial<CandlestickSeriesOptions>);

    // Volume histogram (bottom 25%, overlay on its own price scale)
    const volSeries = chart.addSeries(HistogramSeries, {
      priceScaleId: "vol",
      priceFormat: { type: "volume" },
    } as Partial<HistogramSeriesOptions>);
    chart.priceScale("vol").applyOptions({
      scaleMargins: { top: 0.80, bottom: 0 },
    });
    chart.priceScale("right").applyOptions({
      scaleMargins: { top: 0.02, bottom: 0.25 },
    });

    chartRef.current  = chart;
    candleRef.current = candleSeries;
    volRef.current    = volSeries;

    const ro = new ResizeObserver(() => {
      chart.applyOptions({ width: el.clientWidth, height: el.clientHeight });
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.remove();
      chartRef.current  = null;
      candleRef.current = null;
      volRef.current    = null;
    };
  }, []);

  // Feed data whenever candles change
  useEffect(() => {
    if (!candleRef.current || !volRef.current || candles.length === 0) return;

    const sorted = [...candles].sort((a, b) => a.time - b.time);
    const up   = cssVar("--up")   || "#57BD7D";
    const down = cssVar("--down") || "#E55B5B";

    candleRef.current.setData(
      sorted.map((c) => ({
        time:  c.time as Time,
        open:  c.open,
        high:  c.high,
        low:   c.low,
        close: c.close,
      }))
    );
    volRef.current.setData(
      sorted.map((c) => ({
        time:  c.time as Time,
        value: c.volume,
        color: c.close >= c.open ? up + "99" : down + "99",
      }))
    );

    chartRef.current?.timeScale().fitContent();
  }, [candles]);

  return <div ref={containerRef} className="chart-pane" />;
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function ChartPage() {
  const { symbol } = useParams<{ symbol: string }>();
  const navigate = useNavigate();
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
    refetchInterval: 10_000,
  });

  const candles = history?.candles ?? [];
  const pctUp = (quote?.change_pct ?? 0) >= 0;

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
        </div>
      </div>

      {/* Chart area */}
      <div className="chart-body">
        {isLoading && <Spinner />}
        {isError && (
          <div className="chart-error">
            <span>获取 K 线数据失败</span>
            <Button variant="default" size="sm" onClick={() => refetch()}>重试</Button>
          </div>
        )}
        {!isLoading && !isError && candles.length === 0 && (
          <div className="chart-empty">暂无数据（需要 IBKR Gateway 连接）</div>
        )}
        {candles.length > 0 && <ChartPane candles={candles} />}
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
