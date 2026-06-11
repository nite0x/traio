import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { Search, Trash2, BellOff, LineChart } from "lucide-react";
import { api } from "../api/client";
import { fmt } from "../utils/fmt";
import { useLiveQuotes } from "../hooks/useLiveQuotes";
import {
  Segmented, Spinner, EmptyState, Table, Th, Td, Badge, Button, Input,
  SectionTitle,
} from "../components/ui";
import "./WatchPage.css";

function marketVariant(market: string): "teal" | "gold" | "rust" | "default" {
  if (market === "港股") return "rust";
  if (market === "ETF") return "teal";
  if (market === "Crypto") return "gold";
  return "default";
}

export default function WatchPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");

  const { data: groups = [], isLoading } = useQuery({
    queryKey: ["watchlist-groups"],
    queryFn: api.watchlist.groups,
  });

  const [activeGroup, setActiveGroup] = useState<number | null>(null);
  const groupId = activeGroup ?? groups[0]?.id ?? null;

  const { data: items = [] } = useQuery({
    queryKey: ["watchlist-items", groupId],
    queryFn: () => (groupId ? api.watchlist.items(groupId) : Promise.resolve([])),
    enabled: groupId !== null,
    refetchInterval: 10_000,
  });

  const symbols = items.map((item) => item.symbol).filter(Boolean);
  useLiveQuotes(symbols);
  const { data: quotes = [] } = useQuery({
    queryKey: ["quotes-symbols", symbols.join(",")],
    queryFn: () => (symbols.length ? api.quotes.bySymbols(symbols) : Promise.resolve([])),
    enabled: symbols.length > 0,
    refetchInterval: 30_000,
  });

  const quoteMap = Object.fromEntries(quotes.map((q) => [q.symbol, q]));

  const deleteMut = useMutation({
    mutationFn: ({ groupId, symbol }: { groupId: number; symbol: string }) =>
      api.watchlist.delete(groupId, symbol),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["watchlist-items"] }),
  });

  const filtered = search.trim()
    ? items.filter((i) =>
        i.symbol.toLowerCase().includes(search.toLowerCase()) ||
        i.name.toLowerCase().includes(search.toLowerCase())
      )
    : items;

  if (isLoading) return <Spinner />;

  const groupItems = groups.map((g) => ({ value: String(g.id), label: g.name }));

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">自选</div>
        </div>
      </div>

      {/* Toolbar */}
      <div className="watch-toolbar">
        <Input
          icon={<Search size={15} />}
          placeholder="搜索代码或名称…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="watch-search"
        />
        {groupItems.length > 0 && (
          <Segmented
            items={groupItems}
            value={String(groupId ?? groupItems[0]?.value)}
            onChange={(v) => setActiveGroup(Number(v))}
          />
        )}
      </div>

      {/* Table */}
      <div>
        <SectionTitle
          title={groups.find((g) => g.id === groupId)?.name ?? "自选"}
          hint={filtered.length > 0 ? `${filtered.length} 个标的` : undefined}
        />
        <div style={{ height: 12 }} />

        <Table>
          <thead>
            <tr>
              <Th>代码</Th>
              <Th>名称</Th>
              <Th right>最新价</Th>
              <Th right>涨跌幅</Th>
              <Th right>最高</Th>
              <Th right>最低</Th>
              <Th right>成交量</Th>
              <Th>{""}</Th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((item) => {
              const q = quoteMap[item.symbol];
              const pct = q?.change_pct ?? null;
              const pctClass = pct === null ? "" : pct >= 0 ? "up" : "down";
              const market = item.sec_type === "CRYPTO"
                ? "Crypto"
                : item.exchange?.includes("SEHK") ? "港股"
                : item.sec_type === "ETF" ? "ETF"
                : "US";
              return (
                <tr
                  key={item.symbol}
                  className="watch-row"
                  onClick={() => navigate(`/chart/${item.symbol}`)}
                >
                  <Td mono>
                    <div className="watch-symbol-cell">
                      <span className="watch-symbol">{item.symbol}</span>
                      <Badge label={market} variant={marketVariant(market)} />
                    </div>
                  </Td>
                  <Td>
                    <span className="text-2 truncate" style={{ maxWidth: 180, display: "block" }}>
                      {item.name || "—"}
                    </span>
                  </Td>
                  <Td right mono>{q ? fmt.price(q.last) : "—"}</Td>
                  <Td right mono className={pctClass}>
                    {q ? fmt.pct(q.change_pct) : "—"}
                  </Td>
                  <Td right mono className="text-2">{q ? fmt.price(q.high) : "—"}</Td>
                  <Td right mono className="text-2">{q ? fmt.price(q.low)  : "—"}</Td>
                  <Td right mono className="text-3">{q ? fmt.compact(q.volume) : "—"}</Td>
                  <Td right>
                    <div className="watch-actions" onClick={(e) => e.stopPropagation()}>
                      <Button
                        variant="ghost"
                        size="sm"
                        icon={<LineChart size={13} />}
                        onClick={() => navigate(`/chart/${item.symbol}`)}
                        title="查看 K 线"
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        icon={<BellOff size={13} />}
                        title="添加价格预警"
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        icon={<Trash2 size={13} />}
                        onClick={() => groupId && deleteMut.mutate({ groupId, symbol: item.symbol })}
                        title="从自选移除"
                      />
                    </div>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </Table>
        {filtered.length === 0 && <EmptyState message="暂无自选股" />}
      </div>
    </div>
  );
}
