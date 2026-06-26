import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Play, Square, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import { Card, StatusPill, Button, Toast, SectionTitle } from "../components/ui";
import "./BrokerPage.css";

function fmtAge(seconds: number) {
  if (seconds > 3600) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  if (seconds > 60)   return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return `${seconds}s`;
}

type ToastState = { msg: string; type: "info" | "error" | "success" } | null;

export default function BrokerPage() {
  const qc = useQueryClient();
  const [toast, setToast] = useState<ToastState>(null);

  const show = (msg: string, type: "info" | "error" | "success" = "info") => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 3000);
  };

  const { data: ibkr, isLoading } = useQuery({
    queryKey: ["ibkr-status"],
    queryFn: api.ibkr.status,
    refetchInterval: 8_000,
  });
  const { data: schwab, isLoading: schwabLoading } = useQuery({
    queryKey: ["schwab-status"],
    queryFn: api.schwab.status,
    refetchInterval: 8_000,
  });

  const inv = () => qc.invalidateQueries({ queryKey: ["ibkr-status"] });

  const startMut     = useMutation({ mutationFn: api.ibkr.start,     onSuccess: () => { inv(); show("Gateway 启动中…"); },      onError: (e) => show(String(e), "error") });
  const stopMut      = useMutation({ mutationFn: api.ibkr.stop,      onSuccess: () => { inv(); show("Gateway 已停止"); },        onError: (e) => show(String(e), "error") });
  const reconnectMut = useMutation({ mutationFn: api.ibkr.reconnect, onSuccess: () => { inv(); show("重连中，正在打开登录页…"); }, onError: (e) => show(String(e), "error") });

  const busy = startMut.isPending || stopMut.isPending || reconnectMut.isPending;

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">券商</div>
        </div>
      </div>

      {toast && <Toast message={toast.msg} type={toast.type} />}

      <Card className="broker-card">
        <SectionTitle title="Charles Schwab" />
        <div style={{ height: 20 }} />
        {schwabLoading ? (
          <div className="broker-loading">加载中…</div>
        ) : (
          <div className="broker-info-grid">
            <BrokerRow label="认证状态">
              <StatusPill
                label={schwab?.authenticated ? "已认证" : "未认证"}
                variant={schwab?.authenticated ? "up" : "warn"}
              />
            </BrokerRow>
            <BrokerRow label="实时行情">
              <StatusPill
                label={schwab?.stream.connected ? "已连接" : "等待订阅"}
                variant={schwab?.stream.connected ? "up" : "muted"}
              />
            </BrokerRow>
            <BrokerRow label="订阅股票">
              <span className="mono text-2" style={{ fontSize: 13 }}>
                {schwab?.stream.symbols ?? 0}
              </span>
            </BrokerRow>
            {schwab?.stream.error && (
              <BrokerRow label="最近错误">
                <span className="mono down" style={{ fontSize: 12 }}>
                  {schwab.stream.error}
                </span>
              </BrokerRow>
            )}
          </div>
        )}
      </Card>

      <Card className="broker-card">
        <SectionTitle title="Interactive Brokers Gateway" />
        <div style={{ height: 20 }} />

        {isLoading ? (
          <div className="broker-loading">加载中…</div>
        ) : (
          <>
            <div className="broker-info-grid">
              <BrokerRow label="运行状态">
                <StatusPill
                  label={ibkr?.running ? "运行中" : "已停止"}
                  variant={ibkr?.running ? "up" : "muted"}
                />
              </BrokerRow>
              <BrokerRow label="认证状态">
                <StatusPill
                  label={ibkr?.authenticated ? "已认证" : "未认证"}
                  variant={ibkr?.authenticated ? "up" : "warn"}
                />
              </BrokerRow>
              {ibkr?.account && (
                <BrokerRow label="账户">
                  <span className="mono" style={{ fontSize: 13 }}>{ibkr.account}</span>
                </BrokerRow>
              )}
              {ibkr?.running && (
                <BrokerRow label="运行时长">
                  <span className="mono text-2" style={{ fontSize: 13 }}>
                    {fmtAge(ibkr.session_age_seconds)}
                  </span>
                </BrokerRow>
              )}
            </div>

            <div className="broker-actions">
              {!ibkr?.running ? (
                <Button
                  variant="primary"
                  icon={<Play size={14} />}
                  loading={startMut.isPending}
                  onClick={() => startMut.mutate()}
                  disabled={busy}
                >
                  启动
                </Button>
              ) : (
                <Button
                  variant="danger"
                  icon={<Square size={14} />}
                  loading={stopMut.isPending}
                  onClick={() => stopMut.mutate()}
                  disabled={busy}
                >
                  停止
                </Button>
              )}
              <Button
                icon={<RefreshCw size={14} className={reconnectMut.isPending ? "spin" : ""} />}
                loading={reconnectMut.isPending}
                onClick={() => reconnectMut.mutate()}
                disabled={busy}
              >
                重连
              </Button>
            </div>
          </>
        )}
      </Card>

      <Card className="broker-help-card">
        <SectionTitle title="手动登录" />
        <div style={{ height: 12 }} />
        <p className="broker-help-text text-2">
          Gateway 运行但未认证时，点击上方「重连」会自动打开登录页{" "}
          <code className="mono">https://localhost:5680/sso/Login</code>
          。认证成功后本页面状态将自动更新。
        </p>
      </Card>
    </div>
  );
}

function BrokerRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="broker-row">
      <span className="broker-row__label text-3">{label}</span>
      <div className="broker-row__value">{children}</div>
    </div>
  );
}
