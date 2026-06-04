import { useQuery } from "@tanstack/react-query";
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, ReferenceLine,
} from "recharts";
import { TrendingUp, TrendingDown } from "lucide-react";
import { api, EquityPoint } from "../api/client";
import { fmt } from "../utils/fmt";
import { KpiCard, Spinner, SectionTitle } from "../components/ui";
import "./OverviewPage.css";

function chartDate(ts: string) {
  const d = new Date(ts);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

function CustomTooltip({ active, payload, label }: {
  active?: boolean;
  payload?: { value: number }[];
  label?: string;
}) {
  if (!active || !payload?.length) return null;
  return (
    <div className="chart-tooltip">
      <div className="chart-tooltip__date">{label ? chartDate(label) : ""}</div>
      <div className="chart-tooltip__value mono">{fmt.money(payload[0].value)}</div>
    </div>
  );
}

export default function OverviewPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["equity"],
    queryFn: api.equity,
    refetchInterval: 30_000,
  });

  const s = data?.summary;
  const points: EquityPoint[] = data?.points ?? [];
  const pnlUp = (s?.unrealized_pnl ?? 0) >= 0;

  if (isLoading) return <Spinner />;

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">概览</div>
        </div>
        <div className="page-header__right">
          {s && (
            <div className={`overview-pnl-badge ${pnlUp ? "overview-pnl-badge--up" : "overview-pnl-badge--down"}`}>
              {pnlUp ? <TrendingUp size={14} /> : <TrendingDown size={14} />}
              <span className="mono">{fmt.money(s.unrealized_pnl)}</span>
              <span className="overview-pnl-badge__label">浮动盈亏</span>
            </div>
          )}
        </div>
      </div>

      <div className="overview-kpi-grid">
        <KpiCard
          label="净资产"
          value={fmt.money(s?.net_liquidation)}
          sub={s ? `购买力  ${fmt.money(s.buying_power)}` : undefined}
          accent
        />
        <KpiCard
          label="持仓市值"
          value={fmt.money(s?.gross_position_value)}
        />
        <KpiCard
          label="未实现盈亏"
          value={fmt.money(s?.unrealized_pnl)}
          valueClass={pnlUp ? "up" : "down"}
        />
        <KpiCard
          label="现金余额"
          value={fmt.money(s?.total_cash_value)}
        />
      </div>

      {points.length > 1 && (
        <div className="ui-card overview-chart-card">
          <SectionTitle title="净资产走势" hint={`${points.length} 个数据点`} />
          <div className="overview-chart">
            <ResponsiveContainer width="100%" height={200}>
              <AreaChart data={points} margin={{ top: 8, right: 4, left: 0, bottom: 0 }}>
                <defs>
                  <linearGradient id="eg" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%"   stopColor="#6C5DD3" stopOpacity={0.22} />
                    <stop offset="100%" stopColor="#6C5DD3" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <XAxis
                  dataKey="time"
                  tickFormatter={chartDate}
                  tick={{ fill: "var(--text-3)", fontSize: 11, fontFamily: "var(--font-ui)" }}
                  axisLine={false} tickLine={false}
                />
                <YAxis
                  tickFormatter={(v) => fmt.compact(v)}
                  tick={{ fill: "var(--text-3)", fontSize: 11, fontFamily: "var(--font-ui)" }}
                  axisLine={false} tickLine={false} width={56}
                />
                <Tooltip content={<CustomTooltip />} />
                {points[0] && (
                  <ReferenceLine
                    y={points[0].value}
                    stroke="var(--border-strong)"
                    strokeDasharray="4 3"
                  />
                )}
                <Area
                  type="monotone"
                  dataKey="value"
                  stroke="#6C5DD3"
                  strokeWidth={2}
                  fill="url(#eg)"
                  dot={false}
                  activeDot={{ r: 4, fill: "#6C5DD3", strokeWidth: 0 }}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}

      {data?.warning && (
        <div className="overview-warning">{data.warning}</div>
      )}
    </div>
  );
}
