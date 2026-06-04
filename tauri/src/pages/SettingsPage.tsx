import { useState, useEffect } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { Save, RotateCcw } from "lucide-react";
import { api, Settings } from "../api/client";
import { Card, Button, Toast, SectionTitle } from "../components/ui";
import "./SettingsPage.css";

export default function SettingsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["settings"],
    queryFn: api.settings.get,
  });

  const [draft, setDraft] = useState("");
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [toastMsg, setToastMsg] = useState<{ msg: string; type: "success" | "error" } | null>(null);

  useEffect(() => {
    if (data) setDraft(JSON.stringify(data, null, 2));
  }, [data]);

  const saveMut = useMutation({
    mutationFn: (s: Settings) => api.settings.put(s),
    onSuccess: () => {
      setToastMsg({ msg: "设置已保存", type: "success" });
      setTimeout(() => setToastMsg(null), 2500);
    },
    onError: (e) => {
      setToastMsg({ msg: String(e), type: "error" });
      setTimeout(() => setToastMsg(null), 4000);
    },
  });

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
