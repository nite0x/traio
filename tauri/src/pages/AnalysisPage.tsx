import { useQuery } from "@tanstack/react-query";
import { TrendingUp } from "lucide-react";
import { api, Position, AccountSummary } from "../api/client";
import { fmt } from "../utils/fmt";
import { SectionTitle, Spinner } from "../components/ui";
import "./AnalysisPage.css";

// ── Derived analytics ─────────────────────────────────────────────────────────

interface PositionWithWeight extends Position {
  weight: number;
  annualVolatility?: number;
  ytdPct?: number;
  weekHigh52?: number;
  weekLow52?: number;
  nearHigh52?: boolean;
}

// Tech/growth symbols — used for concentration check
const TECH_SYMBOLS = new Set(["MSFT", "QQQ", "NVDA", "AAPL", "GOOGL", "META", "AMZN", "TSLA", "AMD", "AVGO", "SMCI", "CRWD", "SNOW"]);

function isTech(symbol: string) {
  return TECH_SYMBOLS.has(symbol.toUpperCase());
}

function enrichPositions(positions: Position[], totalValue: number): PositionWithWeight[] {
  return positions.map((p) => ({
    ...p,
    weight: totalValue > 0 ? (p.market_value / totalValue) * 100 : 0,
  }));
}

// ── Sub-components ────────────────────────────────────────────────────────────

function ConcentrationBar({ label, pct, accent }: { label: string; pct: number; accent?: boolean }) {
  return (
    <div className="concentration-row">
      <span className="concentration-label">{label}</span>
      <div className="concentration-track">
        <div
          className={`concentration-fill${accent ? " concentration-fill--accent" : ""}`}
          style={{ width: `${Math.min(pct, 100)}%` }}
        />
      </div>
      <span className="concentration-pct mono">{fmt.pct(pct)}</span>
    </div>
  );
}

type TagVariant = "warn" | "down" | "up" | "muted" | "accent";

function EvalTag({ label, variant }: { label: string; variant: TagVariant }) {
  return <span className={`eval-tag eval-tag--${variant}`}>{label}</span>;
}

function evalTagForPosition(p: PositionWithWeight): { label: string; variant: TagVariant } | null {
  const pnlPct = p.avg_cost > 0 ? ((p.market_price - p.avg_cost) / p.avg_cost) * 100 : 0;
  if (pnlPct <= -15) return { label: `浮亏 ${fmt.pct(pnlPct)}  高位`, variant: "down" };
  if (pnlPct >= 40)  return { label: `浮盈 +${fmt.pct(pnlPct)}  高位`, variant: "warn" };
  if (pnlPct >= 10)  return { label: `YTD +${fmt.pct(pnlPct)}  强势`, variant: "up" };
  if (pnlPct < 0)    return { label: `YTD ${fmt.pct(pnlPct)}  滞涨`, variant: "muted" };
  return { label: `YTD +${fmt.pct(pnlPct)}  利好`, variant: "accent" };
}

interface SuggestionItem {
  index: number;
  title: string;
  body: string;
}

function buildSuggestions(
  positions: PositionWithWeight[],
  summary: AccountSummary,
): SuggestionItem[] {
  const items: SuggestionItem[] = [];
  const totalValue = summary.gross_position_value + summary.total_cash_value;
  const cashPct = totalValue > 0 ? (summary.total_cash_value / totalValue) * 100 : 0;
  const utilization = summary.gross_position_value / (summary.net_liquidation || 1);

  // Tech concentration warning
  const techPct = positions.filter((p) => isTech(p.symbol)).reduce((s, p) => s + p.weight, 0);
  if (techPct > 70) {
    items.push({
      index: items.length + 1,
      title: "科技/成长集中度过高",
      body: `科技+成长仓位合计约 ${fmt.pct(techPct)}，整体与大盘科技板块高度相关，波动风险偏大。可考虑适度分散至防御板块或现金。`,
    });
  }

  // Losing positions
  const bigLosers = positions.filter((p) => {
    const pct = p.avg_cost > 0 ? ((p.market_price - p.avg_cost) / p.avg_cost) * 100 : 0;
    return pct <= -12;
  });
  bigLosers.forEach((p) => {
    const pct = ((p.market_price - p.avg_cost) / p.avg_cost) * 100;
    items.push({
      index: items.length + 1,
      title: `考虑是否止损或减持 ${p.symbol}`,
      body: `浮亏 ${fmt.money(Math.abs(p.unrealized_pnl))}（${fmt.pct(pct)}），均价 ${fmt.price(p.avg_cost)} vs 当前 ${fmt.price(p.market_price)}。持有逻辑需重新确认。`,
    });
  });

  // Big winners — consider locking profit
  const bigWinners = positions.filter((p) => {
    const pct = p.avg_cost > 0 ? ((p.market_price - p.avg_cost) / p.avg_cost) * 100 : 0;
    return pct >= 35 && p.weight > 3;
  });
  bigWinners.forEach((p) => {
    const pct = ((p.market_price - p.avg_cost) / p.avg_cost) * 100;
    items.push({
      index: items.length + 1,
      title: `${p.symbol} 可考虑设置止盈`,
      body: `YTD +${fmt.pct(pct)}，已有浮盈 ${fmt.money(p.unrealized_pnl)}。可考虑锁定部分利润，降低回撤风险。`,
    });
  });

  // Low cash warning
  if (cashPct < 3 && utilization > 0.9) {
    items.push({
      index: items.length + 1,
      title: "保留现金缓冲或降低仓位至 90% 以下",
      body: `当前现金仅 ${fmt.money(summary.total_cash_value)}（${fmt.pct(cashPct)}），仓位使用率 ${fmt.pct(utilization * 100)}，几乎无弹药。若遇回调无法加仓，也无法应对突发风险。适当保持 5~10% 现金仓位是常见做法。`,
    });
  }

  return items;
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function AnalysisPage() {
  const { data: positions = [], isLoading: posLoading } = useQuery({
    queryKey: ["positions"],
    queryFn: api.positions,
    refetchInterval: 30_000,
  });

  const { data: equityData, isLoading: eqLoading } = useQuery({
    queryKey: ["equity"],
    queryFn: api.equity,
    refetchInterval: 30_000,
  });

  if (posLoading || eqLoading) return <Spinner />;

  const summary = equityData?.summary;
  if (!summary) return <Spinner />;

  const totalValue = positions.reduce((s, p) => s + (p.market_value ?? 0), 0);
  const enriched = enrichPositions(positions, totalValue);

  const techPct = enriched.filter((p) => isTech(p.symbol)).reduce((s, p) => s + p.weight, 0);
  const techPositions = enriched.filter((p) => isTech(p.symbol));

  const utilization = summary.gross_position_value / (summary.net_liquidation || 1);
  const suggestions = buildSuggestions(enriched, summary);

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">分析</div>
        </div>
      </div>

      {/* Row 1: Risk + Eval side by side */}
      <div className="analysis-top-row">
        {/* Risk concentration card */}
        <div className="ui-card analysis-risk-card">
          <SectionTitle
            title="风险集中度分析"
            hint={techPct > 70 ? "⚠ 高集中" : undefined}
          />
          <div className="analysis-risk-subtitle text-3">实际科技/成长暴露</div>
          <div className="concentration-list">
            {techPositions.map((p) => (
              <ConcentrationBar key={p.symbol} label={p.symbol} pct={p.weight} />
            ))}
          </div>
          <div className="concentration-total">
            <span className="concentration-total__label text-2">合计科技/成长暴露</span>
            <span className={`concentration-total__value mono${techPct > 70 ? " warn" : ""}`}>
              ~{fmt.pct(techPct)}+
            </span>
          </div>
        </div>

        {/* Per-position eval card */}
        <div className="ui-card analysis-eval-card">
          <SectionTitle title="各仓位综合评估" />
          <div className="eval-list">
            {enriched.map((p) => {
              const tag = evalTagForPosition(p);
              return (
                <div key={p.symbol} className="eval-row">
                  <span className="eval-symbol">{p.symbol}</span>
                  {tag && <EvalTag label={tag.label} variant={tag.variant} />}
                </div>
              );
            })}
          </div>
          <div className={`eval-utilization${utilization > 0.95 ? " eval-utilization--warn" : ""}`}>
            <span className="text-2">整体仓位使用率</span>
            <span className={`mono${utilization > 0.95 ? " warn" : ""}`}>
              {fmt.pct(utilization * 100)}
              {utilization > 0.95 ? "（近满仓）" : ""}
            </span>
          </div>
        </div>
      </div>

      {/* Suggestions */}
      {suggestions.length > 0 && (
        <div className="ui-card">
          <SectionTitle title="调整建议参考" hint="仅供思考，非投资建议" />
          <div className="suggestion-list">
            {suggestions.map((s) => (
              <div key={s.index} className="suggestion-item">
                <div className="suggestion-index">{s.index}</div>
                <div className="suggestion-content">
                  <div className="suggestion-title">{s.title}</div>
                  <div className="suggestion-body text-2">{s.body}</div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {suggestions.length === 0 && positions.length > 0 && (
        <div className="ui-card analysis-ok">
          <div className="analysis-ok__icon"><TrendingUp size={20} /></div>
          <div className="analysis-ok__text">
            <div className="analysis-ok__title">仓位结构健康</div>
            <div className="text-3">暂无需要关注的集中度或风险信号。</div>
          </div>
        </div>
      )}
    </div>
  );
}
