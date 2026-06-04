import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  PieChart, Pie, Cell, Tooltip as PieTooltip, ResponsiveContainer,
  BarChart, Bar, XAxis, YAxis, Tooltip as BarTooltip, ReferenceLine, Cell as BarCell,
} from "recharts";
import { api } from "../api/client";
import { fmt } from "../utils/fmt";
import { KpiCard, Spinner, EmptyState, Table, Th, Td, SectionTitle } from "../components/ui";
import "./HoldingsPage.css";

const PIE_COLORS = [
  "#6C5DD3", "#57BD7D", "#E8A838", "#E55B5B", "#5795D2",
  "#C06E6E", "#7DB8B8", "#9B72CF",
];

function WeightBar({ pct, color }: { pct: number; color: string }) {
  return (
    <div className="weight-bar-track">
      <div
        className="weight-bar-fill"
        style={{ width: `${Math.min(pct, 100)}%`, background: color }}
      />
    </div>
  );
}

function PieCustomTooltip({ active, payload }: { active?: boolean; payload?: { name: string; value: number }[] }) {
  if (!active || !payload?.length) return null;
  return (
    <div className="chart-tooltip">
      <div className="chart-tooltip__date">{payload[0].name}</div>
      <div className="chart-tooltip__value mono">{fmt.pct(payload[0].value)}</div>
    </div>
  );
}

function BarCustomTooltip({ active, payload, label }: { active?: boolean; payload?: { value: number }[]; label?: string }) {
  if (!active || !payload?.length) return null;
  const v = payload[0].value;
  return (
    <div className="chart-tooltip">
      <div className="chart-tooltip__date">{label}</div>
      <div className={`chart-tooltip__value mono ${v >= 0 ? "up" : "down"}`}>{fmt.money(v)}</div>
    </div>
  );
}

export default function HoldingsPage() {
  const navigate = useNavigate();
  const { data: positions = [], isLoading } = useQuery({
    queryKey: ["positions"],
    queryFn: api.positions,
    refetchInterval: 15_000,
  });

  const { data: equityData } = useQuery({
    queryKey: ["equity"],
    queryFn: api.equity,
    refetchInterval: 30_000,
  });

  if (isLoading) return <Spinner />;

  const totalValue   = positions.reduce((s, p) => s + (p.market_value ?? 0), 0);
  const totalPnl     = positions.reduce((s, p) => s + (p.unrealized_pnl ?? 0), 0);
  const totalCash    = equityData?.summary?.total_cash_value ?? 0;
  const netLiq       = equityData?.summary?.net_liquidation ?? 0;
  const pnlUp        = totalPnl >= 0;

  const sorted = [...positions].sort((a, b) => (b.market_value ?? 0) - (a.market_value ?? 0));

  const pieData = sorted.map((p) => ({
    name: p.symbol,
    value: totalValue > 0 ? +((p.market_value / totalValue) * 100).toFixed(2) : 0,
  }));

  const barData = sorted.map((p) => ({
    symbol: p.symbol,
    pnl: +p.unrealized_pnl.toFixed(2),
  }));

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">持仓</div>
        </div>
      </div>

      {/* KPI row */}
      <div className="holdings-kpi-grid">
        <KpiCard
          label="净清算价值"
          value={fmt.money(netLiq)}
          accent
        />
        <KpiCard
          label="持仓市值"
          value={fmt.money(totalValue)}
          sub={`${positions.length} 个持仓`}
        />
        <KpiCard
          label="现金余额"
          value={fmt.money(totalCash)}
        />
        <KpiCard
          label="未实现总盈亏"
          value={fmt.money(totalPnl)}
          valueClass={pnlUp ? "up" : "down"}
        />
      </div>

      {/* Holdings table */}
      <div>
        <SectionTitle title="持仓明细" />
        <div style={{ height: 12 }} />
        <Table>
          <thead>
            <tr>
              <Th>代码</Th>
              <Th>权重分布</Th>
              <Th right>市值</Th>
              <Th right>均价</Th>
              <Th right>现价</Th>
              <Th right>未实现盈亏</Th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((p, i) => {
              const weight = totalValue > 0 ? (p.market_value / totalValue) * 100 : 0;
              const up = p.unrealized_pnl >= 0;
              const color = PIE_COLORS[i % PIE_COLORS.length];
              return (
                <tr
                  key={`${p.symbol}-${p.account}`}
                  className="holdings-row"
                  onClick={() => navigate(`/chart/${p.symbol}`)}
                >
                  <Td mono><span className="holdings-symbol">{p.symbol}</span></Td>
                  <Td>
                    <div className="holdings-weight-cell">
                      <span className="holdings-weight-pct mono text-2">{fmt.pct(weight)}</span>
                      <WeightBar pct={weight} color={color} />
                    </div>
                  </Td>
                  <Td right mono>{fmt.money(p.market_value)}</Td>
                  <Td right mono className="text-2">{fmt.price(p.avg_cost)}</Td>
                  <Td right mono>{fmt.price(p.market_price)}</Td>
                  <Td right mono className={up ? "up" : "down"}>{fmt.money(p.unrealized_pnl)}</Td>
                </tr>
              );
            })}
          </tbody>
        </Table>
        {positions.length === 0 && <EmptyState message="暂无持仓" />}
      </div>

      {/* Charts row */}
      {positions.length > 0 && (
        <div className="holdings-charts-row">
          {/* Pie */}
          <div className="ui-card holdings-pie-card">
            <SectionTitle title="持仓权重" />
            <div className="holdings-pie-wrap">
              <ResponsiveContainer width="100%" height={200}>
                <PieChart>
                  <Pie
                    data={pieData}
                    cx="50%"
                    cy="50%"
                    innerRadius={52}
                    outerRadius={82}
                    paddingAngle={2}
                    dataKey="value"
                  >
                    {pieData.map((_, i) => (
                      <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />
                    ))}
                  </Pie>
                  <PieTooltip content={<PieCustomTooltip />} />
                </PieChart>
              </ResponsiveContainer>
              <div className="holdings-legend">
                {pieData.map((d, i) => (
                  <div key={d.name} className="holdings-legend-item">
                    <span className="holdings-legend-dot" style={{ background: PIE_COLORS[i % PIE_COLORS.length] }} />
                    <span className="holdings-legend-label">{d.name}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* Bar */}
          <div className="ui-card holdings-bar-card">
            <SectionTitle title="各仓盈亏 (USD)" />
            <div className="holdings-bar-wrap">
              <ResponsiveContainer width="100%" height={200}>
                <BarChart data={barData} margin={{ top: 8, right: 4, left: 0, bottom: 0 }}>
                  <XAxis
                    dataKey="symbol"
                    tick={{ fill: "var(--text-3)", fontSize: 11, fontFamily: "var(--font-ui)" }}
                    axisLine={false} tickLine={false}
                  />
                  <YAxis
                    tickFormatter={(v) => fmt.compact(v)}
                    tick={{ fill: "var(--text-3)", fontSize: 11, fontFamily: "var(--font-ui)" }}
                    axisLine={false} tickLine={false} width={52}
                  />
                  <BarTooltip content={<BarCustomTooltip />} cursor={{ fill: "rgba(108,93,211,0.06)" }} />
                  <ReferenceLine y={0} stroke="var(--border-strong)" />
                  <Bar dataKey="pnl" radius={[4, 4, 0, 0]} maxBarSize={40}>
                    {barData.map((d, i) => (
                      <BarCell
                        key={i}
                        fill={d.pnl >= 0 ? "var(--up)" : "var(--down)"}
                        fillOpacity={0.85}
                      />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
