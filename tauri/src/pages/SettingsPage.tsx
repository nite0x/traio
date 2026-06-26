import { useState, useEffect } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { openUrl } from "@tauri-apps/plugin-opener";
import { Save, RotateCcw, ExternalLink } from "lucide-react";
import { api, Settings } from "../api/client";
import { Card, Button, Input, Toast, SectionTitle } from "../components/ui";
import "./SettingsPage.css";

interface SchwabSettings {
  client_id: string;
  client_secret: string;
  redirect_uri: string;
}

const emptySchwabSettings: SchwabSettings = {
  client_id: "",
  client_secret: "",
  redirect_uri: "https://127.0.0.1:8182/callback",
};

interface AlpacaSettings {
  api_key: string;
  api_secret: string;
  base_url: string;
}

const emptyAlpacaSettings: AlpacaSettings = {
  api_key: "",
  api_secret: "",
  base_url: "https://paper-api.alpaca.markets",
};

function readSchwabSettings(settings: Settings): SchwabSettings {
  const schwab = (settings.schwab ?? {}) as Partial<SchwabSettings>;
  return {
    client_id: schwab.client_id ?? "",
    client_secret: schwab.client_secret ?? "",
    redirect_uri: schwab.redirect_uri ?? emptySchwabSettings.redirect_uri,
  };
}

function readAlpacaSettings(settings: Settings): AlpacaSettings {
  const alpaca = (settings.alpaca ?? {}) as Partial<AlpacaSettings>;
  return {
    api_key: alpaca.api_key ?? "",
    api_secret: alpaca.api_secret ?? "",
    base_url: alpaca.base_url ?? emptyAlpacaSettings.base_url,
  };
}

export default function SettingsPage() {
  const queryClient = useQueryClient();
  const { data: schwab, refetch: refetchSchwab } = useQuery({
    queryKey: ["schwab-status"],
    queryFn: api.schwab.status,
    refetchInterval: 5_000,
  });
  const { data: alpaca, refetch: refetchAlpaca } = useQuery({
    queryKey: ["alpaca-status"],
    queryFn: api.alpaca.status,
    refetchInterval: 5_000,
  });
  const { data, isLoading } = useQuery({
    queryKey: ["settings"],
    queryFn: api.settings.get,
  });

  const [draft, setDraft] = useState("");
  const [schwabSettings, setSchwabSettings] = useState<SchwabSettings>(emptySchwabSettings);
  const [alpacaSettings, setAlpacaSettings] = useState<AlpacaSettings>(emptyAlpacaSettings);
  const [schwabCallback, setSchwabCallback] = useState("");
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [toastMsg, setToastMsg] = useState<{ msg: string; type: "success" | "error" } | null>(null);

  useEffect(() => {
    if (data) {
      setDraft(JSON.stringify(data, null, 2));
      setSchwabSettings(readSchwabSettings(data));
      setAlpacaSettings(readAlpacaSettings(data));
    }
  }, [data]);

  const saveMut = useMutation({
    mutationFn: (s: Settings) => api.settings.put(s),
    onSuccess: (_, saved) => {
      queryClient.setQueryData(["settings"], saved);
      setDraft(JSON.stringify(saved, null, 2));
      setSchwabSettings(readSchwabSettings(saved));
      setAlpacaSettings(readAlpacaSettings(saved));
      void refetchAlpaca();
      setToastMsg({ msg: "设置已保存", type: "success" });
      setTimeout(() => setToastMsg(null), 2500);
    },
    onError: (e) => {
      setToastMsg({ msg: String(e), type: "error" });
      setTimeout(() => setToastMsg(null), 4000);
    },
  });

  const saveSchwabSettings = () => {
    if (!data) return;
    saveMut.mutate({
      ...data,
      schwab: schwabSettings,
    });
  };

  const saveAlpacaSettings = () => {
    if (!data) return;
    saveMut.mutate({
      ...data,
      alpaca: alpacaSettings,
    });
  };

  const exchangeMut = useMutation({
    mutationFn: () => api.schwab.exchange(schwabCallback),
    onSuccess: () => {
      setSchwabCallback("");
      void refetchSchwab();
      setToastMsg({ msg: "Schwab 授权成功", type: "success" });
    },
    onError: (e) => setToastMsg({ msg: String(e), type: "error" }),
  });

  const openSchwabAuthorization = async () => {
    try {
      if (!data) throw new Error("后端未连接，请使用 make tauri-dev 启动应用");
      const saved = await api.settings.put({
        ...data,
        schwab: schwabSettings,
      });
      queryClient.setQueryData(["settings"], saved);
      setDraft(JSON.stringify(saved, null, 2));
      const { url } = await api.schwab.oauthUrl();
      await openUrl(url);
    } catch (e) {
      const message = e instanceof TypeError && String(e).includes("Load failed")
        ? "无法连接 Traio 后端，请使用 make tauri-dev 启动应用"
        : String(e);
      setToastMsg({ msg: message, type: "error" });
    }
  };

  const handleChange = (v: string) => {
    setDraft(v);
    try {
      JSON.parse(v);
      setJsonError(null);
    } catch (e) {
      setJsonError(String(e));
    }
  };

  const handleSave = () => {
    try {
      const parsed = JSON.parse(draft);
      saveMut.mutate(parsed);
    } catch (e) {
      setToastMsg({ msg: `JSON 格式错误: ${e}`, type: "error" });
    }
  };

  const handleReset = () => {
    if (data) {
      setDraft(JSON.stringify(data, null, 2));
      setJsonError(null);
    }
  };

  return (
    <div className="page">
      <div className="page-header">
        <div className="page-header__left">
          <div className="page-header__title">设置</div>
        </div>
      </div>

      {toastMsg && <Toast message={toastMsg.msg} type={toastMsg.type} />}

      <Card className="settings-card">
        <div className="settings-card__header">
          <SectionTitle
            title="Charles Schwab"
            hint={schwab?.authenticated
              ? `已授权 · Streamer ${schwab.stream.connected ? "已连接" : "等待订阅"}`
              : "配置 Client ID 与 Client Secret 后完成授权"}
          />
          <div className="settings-card__actions">
            <Button
              variant="primary"
              size="sm"
              icon={<Save size={13} />}
              loading={saveMut.isPending}
              disabled={isLoading || !schwabSettings.client_id.trim() || !schwabSettings.client_secret.trim()}
              onClick={saveSchwabSettings}
            >
              保存 Schwab 配置
            </Button>
            <Button
              variant="default"
              size="sm"
              icon={<ExternalLink size={13} />}
              disabled={
                isLoading ||
                !schwabSettings.client_id.trim() ||
                !schwabSettings.client_secret.trim()
              }
              onClick={openSchwabAuthorization}
            >
              打开授权页
            </Button>
          </div>
        </div>

        <div className="settings-form">
          <label className="settings-field">
            <span className="settings-field__label">Client ID</span>
            <Input
              className="mono"
              value={schwabSettings.client_id}
              onChange={(event) => setSchwabSettings({
                ...schwabSettings,
                client_id: event.target.value,
              })}
              placeholder="Schwab Developer Portal Client ID"
            />
          </label>
          <label className="settings-field">
            <span className="settings-field__label">Client Secret</span>
            <Input
              className="mono"
              type="password"
              autoComplete="off"
              value={schwabSettings.client_secret}
              onChange={(event) => setSchwabSettings({
                ...schwabSettings,
                client_secret: event.target.value,
              })}
              placeholder="Schwab Developer Portal Client Secret"
            />
          </label>
          <label className="settings-field">
            <span className="settings-field__label">回调地址</span>
            <select
              className="input mono"
              value={schwabSettings.redirect_uri}
              onChange={(event) => setSchwabSettings({
                ...schwabSettings,
                redirect_uri: event.target.value,
              })}
            >
              <option value="https://127.0.0.1:8182/callback">
                https://127.0.0.1:8182/callback
              </option>
              <option value="https://127.0.0.1:8183/callback">
                https://127.0.0.1:8183/callback
              </option>
            </select>
            <span className="settings-field__hint">
              必须与 Schwab Developer Portal 中登记的回调地址完全一致
            </span>
          </label>
        </div>

        <div className="settings-callback">
          <Input
            className="mono settings-callback__input"
            value={schwabCallback}
            onChange={(event) => setSchwabCallback(event.target.value)}
            placeholder="授权完成后，粘贴浏览器地址栏中的完整回调 URL"
          />
          <Button
            variant="primary"
            size="sm"
            loading={exchangeMut.isPending}
            disabled={!schwabCallback.trim()}
            onClick={() => exchangeMut.mutate()}
          >
            完成授权
          </Button>
        </div>
        {schwab?.stream.error && (
          <div className="settings-json-error" style={{ marginTop: 12 }}>
            {schwab.stream.error}
          </div>
        )}
      </Card>

      <Card className="settings-card">
        <div className="settings-card__header">
          <SectionTitle
            title="Alpaca Paper"
            hint={alpaca?.configured
              ? `已连接 · 账户 ${alpaca.account_id ?? "—"} · 净值 ${alpaca.equity?.toLocaleString() ?? "—"} ${alpaca.currency ?? ""}`
              : "填写 Paper API Key 与 Secret 后保存"}
          />
          <div className="settings-card__actions">
            <Button
              variant="primary"
              size="sm"
              icon={<Save size={13} />}
              loading={saveMut.isPending}
              disabled={isLoading || !alpacaSettings.api_key.trim() || !alpacaSettings.api_secret.trim()}
              onClick={saveAlpacaSettings}
            >
              保存 Alpaca 配置
            </Button>
          </div>
        </div>

        <div className="settings-form">
          <label className="settings-field">
            <span className="settings-field__label">API Key</span>
            <Input
              className="mono"
              value={alpacaSettings.api_key}
              onChange={(event) => setAlpacaSettings({
                ...alpacaSettings,
                api_key: event.target.value,
              })}
              placeholder="Alpaca Paper API Key ID"
            />
          </label>
          <label className="settings-field">
            <span className="settings-field__label">API Secret</span>
            <Input
              className="mono"
              type="password"
              autoComplete="off"
              value={alpacaSettings.api_secret}
              onChange={(event) => setAlpacaSettings({
                ...alpacaSettings,
                api_secret: event.target.value,
              })}
              placeholder="Alpaca Paper API Secret"
            />
          </label>
          <label className="settings-field">
            <span className="settings-field__label">Endpoint</span>
            <Input
              className="mono"
              value={alpacaSettings.base_url}
              onChange={(event) => setAlpacaSettings({
                ...alpacaSettings,
                base_url: event.target.value,
              })}
              placeholder="https://paper-api.alpaca.markets"
            />
            <span className="settings-field__hint">
              Paper 默认 https://paper-api.alpaca.markets；实盘用 https://api.alpaca.markets
            </span>
          </label>
        </div>
        {alpaca?.error && (
          <div className="settings-json-error" style={{ marginTop: 12 }}>
            {alpaca.error}
          </div>
        )}
      </Card>

      <Card className="settings-card">
        <div className="settings-card__header">
          <SectionTitle title="配置 JSON" hint="编辑后点击保存生效" />
          <div className="settings-card__actions">
            <Button
              variant="ghost"
              size="sm"
              icon={<RotateCcw size={13} />}
              onClick={handleReset}
              disabled={isLoading}
            >
              还原
            </Button>
            <Button
              variant="primary"
              size="sm"
              icon={<Save size={13} />}
              loading={saveMut.isPending}
              onClick={handleSave}
              disabled={isLoading || !!jsonError}
            >
              保存
            </Button>
          </div>
        </div>

        {jsonError && (
          <div className="settings-json-error">{jsonError}</div>
        )}

        <textarea
          className={`settings-textarea mono${jsonError ? " settings-textarea--error" : ""}`}
          value={isLoading ? "加载中…" : draft}
          onChange={(e) => handleChange(e.target.value)}
          disabled={isLoading}
          rows={28}
          spellCheck={false}
        />
      </Card>
    </div>
  );
}
