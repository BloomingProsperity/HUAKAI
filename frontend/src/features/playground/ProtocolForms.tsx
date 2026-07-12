import type { GeminiAction, PlaygroundProtocol, ProtocolFormState } from './types'

interface ProtocolFormsProps {
  protocol: PlaygroundProtocol
  form: ProtocolFormState
  streaming: boolean
  onChange: <K extends keyof ProtocolFormState>(key: K, value: ProtocolFormState[K]) => void
  onStreaming: (value: boolean) => void
}

const GEMINI_ACTIONS: Array<{ value: GeminiAction; label: string }> = [
  { value: 'generateContent', label: 'generateContent' },
  { value: 'countTokens', label: 'countTokens' },
  { value: 'embedContent', label: 'embedContent' },
  { value: 'batchEmbedContents', label: 'batchEmbedContents' },
]

export function ProtocolForms(props: ProtocolFormsProps) {
  const { protocol, form, streaming, onChange, onStreaming } = props
  switch (protocol) {
    case 'chat':
      return (
        <FormStack>
          <TextArea label="System（可选）" value={form.system} rows={2} onChange={(value) => onChange('system', value)} />
          <TextArea label="消息" value={form.input} rows={6} placeholder="输入要发送的内容…" onChange={(value) => onChange('input', value)} />
          <StreamToggle value={streaming} onChange={onStreaming} />
        </FormStack>
      )
    case 'completions':
      return (
        <FormStack>
          <TextArea label="Prompt" value={form.input} rows={7} placeholder="输入补全文本…" onChange={(value) => onChange('input', value)} />
          <NonStreamHint />
        </FormStack>
      )
    case 'messages':
      return (
        <FormStack>
          <TextArea label="System（可选）" value={form.system} rows={2} onChange={(value) => onChange('system', value)} />
          <TextArea label="消息" value={form.input} rows={6} placeholder="输入 Claude 消息…" onChange={(value) => onChange('input', value)} />
          <Field label="Max tokens">
            <input value={form.maxTokens} inputMode="numeric" onChange={(event) => onChange('maxTokens', event.target.value)} style={inputStyle} />
          </Field>
          <StreamToggle value={streaming} onChange={onStreaming} />
        </FormStack>
      )
    case 'responses':
      return (
        <FormStack>
          <TextArea label="Instructions（可选）" value={form.system} rows={2} onChange={(value) => onChange('system', value)} />
          <TextArea label="Input" value={form.input} rows={6} placeholder="输入 Responses 请求内容…" onChange={(value) => onChange('input', value)} />
          <StreamToggle value={streaming} onChange={onStreaming} />
        </FormStack>
      )
    case 'embeddings':
      return (
        <FormStack>
          <TextArea label="输入文本" value={form.input} rows={7} placeholder="输入要向量化的文本…" onChange={(value) => onChange('input', value)} />
          <p style={hintStyle}>响应会显示每条向量的维度与前 8 个值，完整 JSON 仍可展开查看。</p>
          <NonStreamHint />
        </FormStack>
      )
    case 'rerank':
      return (
        <FormStack>
          <TextArea label="Query" value={form.query} rows={2} placeholder="输入排序查询…" onChange={(value) => onChange('query', value)} />
          <TextArea
            label="Documents（每行一篇，最多 1000 篇）"
            value={form.documents}
            rows={8}
            placeholder={'第一篇候选文档\n第二篇候选文档'}
            onChange={(value) => onChange('documents', value)}
          />
          <Field label="Top N（可选）">
            <input value={form.topN} inputMode="numeric" placeholder="留空返回全部" onChange={(event) => onChange('topN', event.target.value)} style={inputStyle} />
          </Field>
          <NonStreamHint />
        </FormStack>
      )
    case 'images':
      return (
        <FormStack>
          <TextArea label="图片描述" value={form.input} rows={6} placeholder="描述要生成的图片…" onChange={(value) => onChange('input', value)} />
          <div style={gridStyle}>
            <Field label="数量">
              <input value={form.imageCount} inputMode="numeric" onChange={(event) => onChange('imageCount', event.target.value)} style={inputStyle} />
            </Field>
            <Field label="尺寸">
              <select value={form.imageSize} onChange={(event) => onChange('imageSize', event.target.value)} style={inputStyle}>
                <option value="1024x1024">1024×1024</option>
                <option value="1024x1792">1024×1792</option>
                <option value="1792x1024">1792×1024</option>
                <option value="512x512">512×512</option>
              </select>
            </Field>
            <Field label="质量">
              <select value={form.imageQuality} onChange={(event) => onChange('imageQuality', event.target.value)} style={inputStyle}>
                <option value="">自动（上游默认）</option>
                <option value="standard">standard</option>
                <option value="hd">hd</option>
                <option value="high">high</option>
                <option value="medium">medium</option>
                <option value="low">low</option>
              </select>
            </Field>
            <Field label="响应格式">
              <select value={form.imageFormat} onChange={(event) => onChange('imageFormat', event.target.value as ProtocolFormState['imageFormat'])} style={inputStyle}>
                <option value="">自动（上游默认）</option>
                <option value="url">url</option>
                <option value="b64_json">b64_json</option>
              </select>
            </Field>
          </div>
          <NonStreamHint />
        </FormStack>
      )
    case 'speech':
      return (
        <FormStack>
          <TextArea label="朗读文本（最多 4096 字符）" value={form.input} rows={7} placeholder="输入要合成的文本…" onChange={(value) => onChange('input', value)} />
          <div style={gridStyle}>
            <Field label="音色">
              <input value={form.voice} placeholder="如 alloy" onChange={(event) => onChange('voice', event.target.value)} style={inputStyle} />
            </Field>
            <Field label="音频格式">
              <select value={form.audioFormat} onChange={(event) => onChange('audioFormat', event.target.value as ProtocolFormState['audioFormat'])} style={inputStyle}>
                {['mp3', 'opus', 'aac', 'flac', 'wav', 'pcm'].map((format) => <option key={format}>{format}</option>)}
              </select>
            </Field>
          </div>
          <NonStreamHint />
        </FormStack>
      )
    case 'gemini':
      return (
        <FormStack>
          <Field label="v1beta 动作">
            <select value={form.geminiAction} onChange={(event) => onChange('geminiAction', event.target.value as GeminiAction)} style={inputStyle}>
              {GEMINI_ACTIONS.map((action) => <option key={action.value} value={action.value}>{action.label}</option>)}
            </select>
          </Field>
          <TextArea
            label="原始 JSON 请求体"
            value={form.rawJSON}
            rows={14}
            mono
            placeholder={'{\n  "contents": []\n}'}
            onChange={(value) => onChange('rawJSON', value)}
          />
          <p style={hintStyle}>模型取自上方模型框并写入 URL；此处只填写动作对应的原始 JSON body。</p>
          <NonStreamHint />
        </FormStack>
      )
  }
}

function FormStack({ children }: { children: React.ReactNode }) {
  return <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>{children}</div>
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      <span>{label}</span>
      {children}
    </label>
  )
}

function TextArea(props: {
  label: string
  value: string
  rows: number
  placeholder?: string
  mono?: boolean
  onChange: (value: string) => void
}) {
  return (
    <Field label={props.label}>
      <textarea
        value={props.value}
        rows={props.rows}
        placeholder={props.placeholder}
        onChange={(event) => props.onChange(event.target.value)}
        style={{ ...inputStyle, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', resize: 'vertical', fontFamily: props.mono ? 'var(--hk-font-mono)' : 'inherit' }}
      />
    </Field>
  )
}

function StreamToggle({ value, onChange }: { value: boolean; onChange: (value: boolean) => void }) {
  return (
    <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-500)' }}>
      <input type="checkbox" checked={value} onChange={(event) => onChange(event.target.checked)} />
      流式逐字显示
    </label>
  )
}

function NonStreamHint() {
  return <span style={hintStyle}>该协议在调试台中使用非流式请求。</span>
}

const inputStyle: React.CSSProperties = {
  minHeight: 34,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-sm)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  fontSize: 13,
  width: '100%',
}

const hintStyle: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-300)' }
const gridStyle: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
  gap: 'var(--hk-space-3)',
}
