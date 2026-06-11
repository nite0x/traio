import { useQuery } from "@tanstack/react-query";
import { TrendingUp, TrendingDown } from "lucide-react";
import { api, Position } from "../api/client";
import { fmt } from "../utils/fmt";
import { useLiveQuotes } from "../hooks/useLiveQuotes";
import { KpiCard, Spinner, EmptyState } from "../components/ui";
import "./TodayPage.css";

// ── Helpers ───────────────────────────────────────────────────────────────────

function pnlPct(p: Position) {
  return p.avg_cost > 0 ? ((p.market_price - p.avg_cost) / p.avg_cost) * 100 : 0;
}

// Estimate annualized volatility from daily move — rough proxy only
function volLabel(dayPct: number): string {
  const annualized = Math.abs(dayPct) * Math.sqrt(252);
  return `${annualized.toFixed(1)}%`;
}

function Range52({ low, high, current }: { low: number; high: number; current: number }) {
  const span = high - low;
  const pos = span > 0 ? ((current - low) / span) * 100 : 50;
  return (
    <div className="range52">
      <span className="range52__label mono text-3">{fmt.price(low)}</span>
      <div className="range52__track">
        <div className="range52__fill" style={{ width: `${Math.min(Math.max(pos, 2), 98)}%` }} />
        <div className="range52__dot" style={{ left: `${Math.min(Math.max(pos, 2), 98)}%` }} />
      </div>
      <span className="range52__label mono text-3">{fmt.price(high)}</span>
    </div>
  );
}

interface PositionCardProps {
  p: Position;
  dayChange?: number;
  dayChangePct?: number;
  bid?: number;
  ask?: number;
  weekHigh52?: number;
  weekLow52?: number;
  annualVol?: number;
  marketClosed?: boolean;
}

function PositionCard({
  p,
  dayChange,
  dayChangePct,
  bid,
  ask,
  weekHigh52,
  weekLow52,
  annualVol,
  marketClosed,
}: PositionCardProps) {
  const up = (dayChangePct ?? 0) >= 0;
  const ytdUp = pnlPct(p) >= 0;
  const near52High = weekHigh52 && p.market_price >= weekHigh52 * 0.97;

  return (
    <div className={`today-card ui-card${marketClosed ? " today-card--closed" : ""}`}>
      {/* Header row */}
      <div className="today-card__header">
        <div className="today-card__symbol-wrap">
          <span className="today-card__symbol">{p.symbol}</span>
          {marketClosed && <span className="today-card__closed-badge">已收盘</span>}
          {near52High && !marketClosed && <span className="today-card__high-badge">近52周高</span>}
        </div>
        <div className="today-card__price-wrap">
          <span className="today-card__price mono">{fmt.price(p.market_price)}</span>
          {dayChangePct !== undefined && (
            <span className={`today-card__day-change ${up ? "up" : "down"}`}>
              {up ? <TrendingUp size={13} /> : <TrendingDown size={13} />}
              <span className="mono">{up ? "+" : ""}{fmt.pct(dayChangePct)}</span>
              {dayChange !== undefined && (
                <span className="mono today-card__day-abs">
                  {up ? "+" : ""}{fmt.money(dayChange * p.quantity)}
                </span>
              )}
            </span>
          )}
        </div>
      </div>

      {/* Sub-row: company + quantity */}
      <div className="today-card__sub text-3">
        {p.quantity} 股 · 均价 {fmt.price(p.avg_cost)}
      </div>

      {/* Metrics grid */}
      <div className="today-card__metrics">
        <div className="today-metric">
          <span className="today-metric__label">未实现盈亏</span>
          <span className={`today-metric__value mono ${ytdUp ? "up" : "down"}`}>
            {ytdUp ? "+" : ""}{fmt.money(p.unrealized_pnl)}
          </span>
        </div>
        <div className="today-metric">
          <span className="today-metric__label">年初至今</span>
          <span className={`today-metric__value mono ${ytdUp ? "up" : "down"}`}>
            {ytdUp ? "+" : ""}{fmt.pct(pnlPct(p))}
          </span>
        </div>
        {annualVol !== undefined && (
          <div className="today-metric">
            <span className="today-metric__label">年化波动率</span>
            <span className="today-metric__value mono warn">{fmt.pct(annualVol)}</span>
          </div>
        )}
        {bid !== undefined && ask !== undefined && (
          <div className="today-metric">
            <span className="today-metric__label">Bid / Ask</span>
            <span className="today-metric__value mono text-2">
              {fmt.price(bid)} / {fmt.price(ask)}
            </span>
          </div>
        )}
      </div>

      {/* 52-week range */}
      {weekHigh52 && weekLow52 && (
        <div className="today-card__range">
          <span className="today-metric__label">52周区间</span>
          <Range52 low={weekLow52} high={weekHigh52} current={p.market_price} />
        </div>
      )}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function TodayPage() {
  const { data: positions = [], isLoading: posLoading } = useQuery({
    queryKey: ["positions"],
    queryFn: api.positions,
    refetchInterval: 15_000,
  });

  const symbols = positions.map((position) => position.symbol).filter(Boolean);
  useLiveQuotes(symbols);
  const { data: quotes = [] } = useQuery({
    queryKey: ["quotes-symbols", symbols.join(",")],
    queryFn: () => (symbols.length ? api.quotes.bySymbols(symbols) : Promise.resolve([])),
    enabled: symbols.length > 0,
    refetchInterval: 30_000,
  });

  if (posLoading) return <Spinner />;
  if (positions.length === 0) return <EmptyState message="暂无持仓" />;

  const quoteMap = Object.fromEntries(quotes.map((q) => [q.symbol, q]));

  // Today's aggregate PnL = sum of (dayChange * quantity) for positions with quotes
  const todayTotal = positions.reduce((s, p) => {
    const q = quoteMap[p.symbol];
    return s + (q ? q.change * p.quantity : 0);
  }, 0);
  const todayUp = todayTotal >= 0;

  // Highest volatility position (by |change_pct|)
  const byVol = [...positions].sort((a, b) => {
    const qa = quoteMap[a.symbol];
    const qb = quoteMap[b.symbol];
    return Math.abs(qb?.change_pct ?? 0) - Math.abs(qa?.change_pct ?? 0);
  });
  const topVolPos = byVol[0];
  const topVolQuote = topVolPos ? quoteMap[topVolPos.symbol] : undefined;

  // Best and worst performers today
  const withQuotes = positions.filter((p) => quoteMap[p.symbol]);
  const best  = withQuotes.length ? withQuotes.reduce((a, b) =>
    (quoteMap[a.symbol]?.change_pct ?? 0) > (quoteMap[b.symbol]?.change_pct ?? 0) ? a : b
  ) : null;
  const worst = withQuotes.length ? withQuotes.reduce((a, b) =>
    (quoteMap[a.symbol]?.change_pct ?? 0) < (quoteMap[b.symbol]?.change_pct ?? 0) ? a : b
  ) : null;

  // Sort cards: biggest market value first
  const sorted = [...positions].sort((a, b) => (b.market_value ?? 0) - (a.market_value ?? 0));

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">今日</div>
        </div>
      </div>

      {/* Summary KPIs */}
      <div className="today-kpi-grid">
        <KpiCard
          label="今日总盈亏变化"
          value={`${todayUp ? "+" : ""}${fmt.money(todayTotal)}`}
          valueClass={todayUp ? "up" : "down"}
          accent={todayUp}
        />
        {topVolPos && topVolQuote && (
          <KpiCard
            label="最高波动率仓位"
            value={topVolPos.symbol}
            sub={`${volLabel(topVolQuote.change_pct)} 年化`}
            valueClass="warn"
          />
        )}
        {best && quoteMap[best.symbol] && (
          <KpiCard
            label="今日涨幅最大"
            value={`${best.symbol} +${fmt.pct(quoteMap[best.symbol].change_pct)}`}
            valueClass="up"
          />
        )}
        {worst && quoteMap[worst.symbol] && (
          <KpiCard
            label="今日跌幅最大"
            value={`${worst.symbol} ${fmt.pct(quoteMap[worst.symbol].change_pct)}`}
            valueClass="down"
          />
        )}
      </div>

      {/* Section */}
      <div className="today-section-title">实时持仓详情</div>

      {/* Position cards */}
      <div className="today-cards-grid">
        {sorted.map((p) => {
          const q = quoteMap[p.symbol];
          // Rough 52w range: ±30% from avg cost as fallback when no market data
          const weekHigh52 = q?.high ? q.high * 1.6 : undefined;
          const weekLow52  = q?.low  ? q.low  * 0.6 : undefined;
          const annualVol  = q ? Math.abs(q.change_pct) * Math.sqrt(252) : undefined;

          return (
            <PositionCard
              key={`${p.symbol}-${p.account}`}
              p={p}
              dayChange={q?.change}
              dayChangePct={q?.change_pct}
              bid={q?.bid}
              ask={q?.ask}
              weekHigh52={weekHigh52}
              weekLow52={weekLow52}
              annualVol={annualVol}
              marketClosed={!q || q.last === 0}
            />
          );
        })}
      </div>
    </div>
  );
}
