import { FormEvent, useState } from "react";
import type { ReactNode } from "react";
import {
  CheckCircle2,
  FlaskConical,
  Globe2,
  KeyRound,
  LockKeyhole,
  Save,
  ServerCog,
  ShieldCheck,
  TestTube2,
  type LucideIcon,
} from "lucide-react";
import { getDefaultProviderConfig, saveProviderConfig, testProviderConnection } from "../api/client";
import type { ProviderConfig } from "../types";

export function ConfigPage() {
  const [config, setConfig] = useState<ProviderConfig>(getDefaultProviderConfig);
  const [notice, setNotice] = useState("配置仅保存在当前原型流程中，不包含真实密钥。");
  const [isTesting, setIsTesting] = useState(false);

  function update<K extends keyof ProviderConfig>(key: K, value: ProviderConfig[K]) {
    setConfig((current) => ({ ...current, [key]: value }));
  }

  async function handleSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const saved = await saveProviderConfig(config);
    setConfig(saved);
    setNotice("配置已保存到 Cribug 后端。");
  }

  async function handleTest() {
    setIsTesting(true);
    const result = await testProviderConnection(config);
    setNotice(result.mock ? `Mock: ${result.answer}` : `连接成功: ${result.provider}/${result.model}`);
    setIsTesting(false);
  }

  return (
    <main className="mx-auto max-w-6xl space-y-5">
      <section className="access-hero">
        <div>
          <div className="signal-label mb-4">
            <ServerCog className="h-4 w-4" />
            能力接入舱
          </div>
          <h1 className="text-2xl font-semibold text-textMain">模型与接口配置</h1>
          <p className="mt-2 max-w-2xl text-sm leading-7 text-textMuted">
            Provider、模型选择、API Key 环境变量、Mock 回退策略。配置直接写入后端。
          </p>
        </div>
        <div className="access-notice">
          <CheckCircle2 className="h-4 w-4" />
          {notice}
        </div>
      </section>

      <form onSubmit={handleSave} className="grid gap-5 lg:grid-cols-[1fr_380px]">
        <section className="space-y-5">
          <ConfigGroup icon={ServerCog} title="模型供应商接入" subtitle="Provider / Chat Model / Embedding / Base URL">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="模型供应商">
                <select className="input" value={config.provider} onChange={(event) => update("provider", event.target.value)}>
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="local">本地模型</option>
                  <option value="cribug">Cribug API</option>
                </select>
              </Field>
              <Field label="Chat Model">
                <input className="input" value={config.chat_model} onChange={(event) => update("chat_model", event.target.value)} />
              </Field>
              <Field label="Embedding Model">
                <input className="input" value={config.embedding_model} onChange={(event) => update("embedding_model", event.target.value)} />
              </Field>
              <Field label="Base URL Override">
                <input className="input" value={config.base_url_override || ""} onChange={(event) => update("base_url_override", event.target.value)} placeholder="留空使用默认地址" />
              </Field>
            </div>
          </ConfigGroup>

          <ConfigGroup icon={KeyRound} title="敏感凭证传递" subtitle="API Key 环境变量名，前端仅传递变量名给后端解析">
            <Field label="API Key 环境变量">
              <input
                className="input"
                type="text"
                value={config.api_key_env || ""}
                onChange={(event) => update("api_key_env", event.target.value)}
                placeholder="如 OPENAI_API_KEY、ANTHROPIC_API_KEY"
              />
            </Field>
            <div className="mt-3 flex items-center gap-2 rounded-md border border-bronze/25 bg-bronze/10 p-3 text-sm text-bronze">
              <LockKeyhole className="h-4 w-4" />
              敏感信息应由后端环境变量托管，前端只传递变量名。
            </div>
          </ConfigGroup>

          <ConfigGroup icon={ShieldCheck} title="执行边界" subtitle="Mock 回退与真实 LLM 要求">
            <div className="grid gap-4 md:grid-cols-2">
              <label className="flex items-center gap-3 rounded-md border border-white/10 bg-white/[0.03] px-4 py-3">
                <input type="checkbox" checked={config.allow_mock_fallback} onChange={(event) => update("allow_mock_fallback", event.target.checked)} />
                <span className="text-sm text-textMain">允许 Mock 回退</span>
              </label>
              <label className="flex items-center gap-3 rounded-md border border-white/10 bg-white/[0.03] px-4 py-3">
                <input type="checkbox" checked={config.require_real} onChange={(event) => update("require_real", event.target.checked)} />
                <span className="text-sm text-textMain">强制真实 LLM</span>
              </label>
            </div>
          </ConfigGroup>
        </section>

        <aside className="access-sidebar">
          <Toggle icon={Globe2} label="允许 Mock 回退" checked={config.allow_mock_fallback} onChange={(value) => update("allow_mock_fallback", value)} />
          <Toggle icon={FlaskConical} label="强制真实调用" checked={config.require_real} onChange={(value) => update("require_real", value)} />

          <div className="grid gap-3 pt-2">
            <button type="submit" className="primary-button justify-center">
              <Save className="h-4 w-4" />
              保存配置
            </button>
            <button type="button" className="secondary-button justify-center" onClick={handleTest} disabled={isTesting}>
              <TestTube2 className="h-4 w-4" />
              {isTesting ? "测试中" : "测试连接"}
            </button>
          </div>
        </aside>
      </form>
    </main>
  );
}

function ConfigGroup({ icon: Icon, title, subtitle, children }: { icon: LucideIcon; title: string; subtitle: string; children: ReactNode }) {
  return (
    <section className="access-chamber">
      <div className="mb-4 flex items-start gap-3">
        <span className="chamber-icon">
          <Icon className="h-4 w-4" />
        </span>
        <div>
          <h2 className="text-base font-semibold text-textMain">{title}</h2>
          <p className="mt-1 text-xs text-textMuted">{subtitle}</p>
        </div>
      </div>
      {children}
    </section>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-2 block text-sm text-textMuted">{label}</span>
      {children}
    </label>
  );
}

function Toggle({ icon: Icon, label, checked, onChange }: { icon: LucideIcon; label: string; checked: boolean; onChange: (value: boolean) => void }) {
  return (
    <label className="toggle-row">
      <span className="flex items-center gap-2 text-sm text-textMain">
        <Icon className="h-4 w-4 text-energy" />
        {label}
      </span>
      <input className="toggle" type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
    </label>
  );
}
