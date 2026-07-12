export type PlaygroundProtocol =
  | 'chat'
  | 'completions'
  | 'messages'
  | 'responses'
  | 'embeddings'
  | 'rerank'
  | 'images'
  | 'speech'
  | 'gemini'

export type GeminiAction = 'generateContent' | 'countTokens' | 'embedContent' | 'batchEmbedContents'

export interface ModelObject {
  id: string
  owned_by?: string
}

export interface ModelListResponse {
  object: string
  data: ModelObject[]
}

export type ChatRole = 'system' | 'user' | 'assistant'

export interface ChatMessage {
  role: ChatRole
  content: string
}

export interface ChatRequest {
  model: string
  messages: ChatMessage[]
  stream: boolean
}

export interface ChatUsage {
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
  input_tokens?: number
  output_tokens?: number
}

export interface ChatChoice {
  text?: string
  message?: { role?: string; content?: string }
  finish_reason?: string
}

export interface ChatResponse {
  id?: string
  model?: string
  choices?: ChatChoice[]
  usage?: ChatUsage
}

export interface ProtocolFormState {
  system: string
  input: string
  query: string
  documents: string
  maxTokens: string
  topN: string
  imageCount: string
  imageSize: string
  imageQuality: string
  imageFormat: '' | 'url' | 'b64_json'
  voice: string
  audioFormat: 'mp3' | 'opus' | 'aac' | 'flac' | 'wav' | 'pcm'
  geminiAction: GeminiAction
  rawJSON: string
}

export type JSONRecord = Record<string, unknown>

export interface ProtocolRequestPlan {
  protocol: PlaygroundProtocol
  path: string
  body: JSONRecord
  stream: boolean
}

export interface ParsedSSEEvent {
  done: boolean
  content: string
  payload?: unknown
}

export interface AudioResponse {
  blob: Blob
  contentType: string
}

export interface RerankResultView {
  index: number
  score: number
  document?: unknown
}
