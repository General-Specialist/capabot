const BASE = '/api'

export interface Agent {
  id: string
  name: string
  provider: string
  model: string
  skills: string[]
  tools: string[]
  max_tokens: number
  temperature: number
}

export interface Conversation {
  id: string
  channel: string
  user_id: string
  created_at: string
  updated_at: string
  message_count: number
}

export interface Message {
  id: number
  session_id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  tool_name?: string
  token_count: number
  created_at: string
}

export interface Skill {
  name: string
  description: string
  version: string
  has_instructions: boolean
}

export interface ProviderInfo {
  name: string
  models: { id: string; name: string; context_window: number }[]
}

export interface HealthStatus {
  status: string
  version: string
  uptime_seconds: number
}

export interface ChatResponse {
  response: string
  tool_calls: number
  iterations: number
  usage: { input_tokens: number; output_tokens: number }
  stop_reason: string
}

export interface StreamChunk {
  delta?: string
  done?: boolean
  tool_call?: { id: string; name: string }
  error?: string
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path)
  if (!res.ok) throw new Error(`API error ${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`API error ${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}

export const api = {
  health: () => get<HealthStatus>('/health'),
  agents: () => get<Agent[]>('/agents'),
  conversations: (limit = 50) => get<Conversation[]>(`/conversations?limit=${limit}`),
  conversation: (id: string) => get<{ session: Conversation; messages: Message[] }>(`/conversations/${id}`),
  skills: () => get<Skill[]>('/skills'),
  providers: () => get<ProviderInfo[]>('/providers'),
  chat: (text: string, sessionId?: string) =>
    post<ChatResponse>('/chat', { text, session_id: sessionId }),

  chatStream(
    text: string,
    sessionId: string | undefined,
    onChunk: (chunk: StreamChunk) => void,
    signal?: AbortSignal
  ): Promise<void> {
    return fetch(BASE + '/chat/stream', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, session_id: sessionId }),
      signal,
    }).then(async (res) => {
      if (!res.ok) throw new Error(`Stream error ${res.status}`)
      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() ?? ''
        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const chunk = JSON.parse(line.slice(6)) as StreamChunk
              onChunk(chunk)
            } catch {
              // ignore malformed SSE lines
            }
          }
        }
      }
    })
  },
}
