'use client';

// 采样参数面板：temperature / max_tokens / top_p，每项带启停开关。
// 参数清单与"逐项启停"交互取自 new-api playground（ParameterControl + parameterEnabled）；
// 关闭某项即不写入请求体。top_p 作为可选增强同样透传到上游。
// 顶部展示当前所选模型的 context_length / capabilities / max_output_tokens（来自 /v1/models）。

import { Sliders, Thermometer, Hash, Target, Check, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import type { ModelObject } from '@/lib/api/models';
import type { SamplingParams, ParamEnabled } from './types';

function ToggleDot({
  on,
  disabled,
  onClick,
}: {
  on: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'flex size-4 items-center justify-center rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-50',
        on
          ? 'bg-primary-600 text-white'
          : 'bg-accent-200 text-accent-500 dark:bg-accent-700 dark:text-accent-400',
      )}
      title={on ? '已启用（点击关闭，不写入请求体）' : '已关闭（点击启用）'}
    >
      {on ? <Check className="size-2.5" /> : <X className="size-2.5" />}
    </button>
  );
}

function fmtNum(n: number | undefined): string {
  return n === undefined ? '–' : n.toLocaleString('zh-CN');
}

export function ParamsPanel({
  params,
  enabled,
  onParamChange,
  onToggle,
  selectedModel,
  disabled,
}: {
  params: SamplingParams;
  enabled: ParamEnabled;
  onParamChange: <K extends keyof SamplingParams>(key: K, value: SamplingParams[K]) => void;
  onToggle: (key: keyof ParamEnabled) => void;
  selectedModel?: ModelObject;
  disabled?: boolean;
}) {
  const caps = selectedModel?.capabilities
    ? Object.entries(selectedModel.capabilities).filter(([, v]) => v).map(([k]) => k)
    : [];

  return (
    <div className="space-y-4">
      {/* 模型元信息 */}
      {selectedModel && (
        <div className="space-y-2 rounded-lg border border-accent-200 bg-accent-50 p-3 dark:border-accent-800 dark:bg-accent-950/40">
          <div className="flex items-center gap-2 text-xs font-medium text-accent-600 dark:text-accent-300">
            <Sliders className="size-3.5 text-primary-600 dark:text-primary-300" />
            模型能力
          </div>
          <div className="grid grid-cols-2 gap-2 text-[11px]">
            <div className="rounded-md bg-white px-2 py-1.5 dark:bg-accent-900/60">
              <div className="text-accent-400 dark:text-accent-500">上下文窗口</div>
              <div className="font-mono font-semibold text-accent-900 dark:text-accent-100">
                {fmtNum(selectedModel.context_length)}
              </div>
            </div>
            <div className="rounded-md bg-white px-2 py-1.5 dark:bg-accent-900/60">
              <div className="text-accent-400 dark:text-accent-500">最大输出</div>
              <div className="font-mono font-semibold text-accent-900 dark:text-accent-100">
                {fmtNum(selectedModel.max_output_tokens)}
              </div>
            </div>
          </div>
          {caps.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {caps.map((c) => (
                <Badge key={c} variant="outline" className="text-[10px]">
                  {c}
                </Badge>
              ))}
            </div>
          )}
          {selectedModel.pricing &&
            (selectedModel.pricing.input_per_token || selectedModel.pricing.output_per_token) && (
              <div className="text-[10px] text-accent-400 dark:text-accent-500">
                价格/token：入 {selectedModel.pricing.input_per_token ?? '–'} · 出{' '}
                {selectedModel.pricing.output_per_token ?? '–'}
              </div>
            )}
        </div>
      )}

      {/* Temperature */}
      <ParamRow
        icon={<Thermometer className="size-3.5" />}
        label="Temperature"
        hint="控制输出随机性"
        value={params.temperature}
        on={enabled.temperature}
        disabled={disabled}
        onToggle={() => onToggle('temperature')}
      >
        <input
          type="range"
          min={0}
          max={2}
          step={0.1}
          value={params.temperature}
          disabled={disabled || !enabled.temperature}
          onChange={(e) => onParamChange('temperature', Number(e.target.value))}
          className="w-full accent-primary-600 disabled:opacity-50"
        />
      </ParamRow>

      {/* Top P */}
      <ParamRow
        icon={<Target className="size-3.5" />}
        label="Top P"
        hint="核采样多样性"
        value={params.topP}
        on={enabled.topP}
        disabled={disabled}
        onToggle={() => onToggle('topP')}
      >
        <input
          type="range"
          min={0}
          max={1}
          step={0.05}
          value={params.topP}
          disabled={disabled || !enabled.topP}
          onChange={(e) => onParamChange('topP', Number(e.target.value))}
          className="w-full accent-primary-600 disabled:opacity-50"
        />
      </ParamRow>

      {/* Max Tokens */}
      <ParamRow
        icon={<Hash className="size-3.5" />}
        label="Max Tokens"
        hint="单次回复上限"
        on={enabled.maxTokens}
        disabled={disabled}
        onToggle={() => onToggle('maxTokens')}
      >
        <input
          type="number"
          min={1}
          step={1}
          value={params.maxTokens}
          disabled={disabled || !enabled.maxTokens}
          onChange={(e) => {
            const n = Number(e.target.value);
            if (Number.isFinite(n) && n > 0) onParamChange('maxTokens', Math.floor(n));
          }}
          className="w-full rounded-md border border-accent-200 bg-white px-2 py-1 text-sm text-accent-900 outline-none focus:border-primary-500 disabled:opacity-50 dark:border-accent-800 dark:bg-accent-950/60 dark:text-accent-100"
        />
      </ParamRow>
    </div>
  );
}

function ParamRow({
  icon,
  label,
  hint,
  value,
  on,
  disabled,
  onToggle,
  children,
}: {
  icon: React.ReactNode;
  label: string;
  hint: string;
  value?: number;
  on: boolean;
  disabled?: boolean;
  onToggle: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className={cn('space-y-1.5', !on && 'opacity-60')}>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5 text-xs font-medium text-accent-600 dark:text-accent-300">
          <span className="text-accent-400">{icon}</span>
          {label}
          {value !== undefined && (
            <span className="rounded bg-accent-100 px-1.5 py-0.5 font-mono text-[10px] text-accent-600 dark:bg-accent-800 dark:text-accent-300">
              {value}
            </span>
          )}
        </div>
        <ToggleDot on={on} disabled={disabled} onClick={onToggle} />
      </div>
      <p className="text-[10px] text-accent-400 dark:text-accent-500">{hint}</p>
      {children}
    </div>
  );
}
