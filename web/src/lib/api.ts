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
  title: string
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
  tool_input?: string
  token_count: number
  created_at: string
}

export interface Skill {
  name: string
  description: string
  version: string
  instructions: string
  removable: boolean
  tier: number // 1=prompt-only, 2=native Go, 3=WASM
}

export interface CatalogSkill {
  name: string
  description: string
  version: string
  path: string
  html_url: string
  downloads: number
  stars: number
}

export interface InstallResult {
  skill_name: string
  tier: number
  success: boolean
  warnings: string[]
}

export interface ProviderInfo {
  name: string
  models: { id: string; name: string; context_window: number }[]
}

export interface HealthStatus {
  status: string
  version: string
  uptime_seconds: number
  skills_loaded: number
  providers_count: number
}

export interface ChatResponse {
  response: string
  tool_calls: number
  iterations: number
  usage: { input_tokens: number; output_tokens: number }
  stop_reason: string
}

// LLMMessage is the full wire format sent to/from the backend for chat history.
// Matches internal/llm.ChatMessage JSON fields.
export interface LLMMessage {
  role: 'user' | 'assistant' | 'tool'
  content?: string
  tool_calls?: { id: string; name: string; input: unknown }[]
  tool_result?: { tool_use_id: string; content: string; is_error?: boolean }
}

export interface StreamChunk {
  // agent event fields
  event?: string
  content?: string
  thinking?: string
  tool_name?: string
  tool_id?: string
  tool_input?: Record<string, unknown>
  is_error?: boolean
  iteration?: number
  // completion fields
  session_id?: string
  done?: boolean
  tool_calls?: number
  iterations?: number
  usage?: { input_tokens: number; output_tokens: number }
  error?: string
}

export interface Automation {
  id: number
  name: string
  rrule: string
  start_at: string | null
  end_at: string | null
  prompt: string
  skill_name: string
  enabled: boolean
  last_run_at: string | null
  next_run_at: string | null
  created_at: string
  updated_at: string
}

export interface ProviderKeys {
  anthropic: string
  openai: string
  gemini: string
  openrouter: string
}

export interface AutomationRun {
  id: number
  automation_id: number
  started_at: string
  finished_at: string | null
  status: 'running' | 'success' | 'error' | 'stopped'
  response: string
  error: string
}

export interface ToolExecution {
  id: number
  tool_name: string
  input: string
  output: string
  duration_ms: number
  success: boolean
}

export interface TraceMessage {
  id: number
  role: 'user' | 'assistant' | 'tool'
  content: string
  tool_name?: string
  tool_input?: string
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path)
  if (!res.ok) throw new Error(`API error ${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}

async function del<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path, { method: 'DELETE' })
  if (!res.ok) throw new Error(`API error ${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}

async function put<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
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
  skillsCatalog: (q?: string, limit = 200, offset = 0) => {
    const params = new URLSearchParams()
    if (q) params.set('q', q)
    params.set('limit', String(limit))
    params.set('offset', String(offset))
    return get<CatalogSkill[]>(`/skills/catalog?${params}`)
  },
  skillsInstall: (name: string) => post<InstallResult>('/skills/install', { name }),
  providers: () => get<ProviderInfo[]>('/providers'),
  automations: () => get<Automation[]>('/automations'),
  automationCreate: (data: { name: string; rrule: string; start_at?: string | null; end_at?: string | null; prompt: string; skill_name?: string; enabled?: boolean }) =>
    post<Automation>('/automations', data),
  automationUpdate: (id: number, data: Partial<{ name: string; rrule: string; start_at: string | null; end_at: string | null; prompt: string; skill_name: string; enabled: boolean }>) =>
    put<Automation>(`/automations/${id}`, data),
  automationDelete: (id: number) => del<{ success: boolean }>(`/automations/${id}`),
  automationTrigger: (id: number) => post<{ triggered: boolean }>(`/automations/${id}/trigger`, {}),
  automationRuns: (id: number) => get<AutomationRun[]>(`/automations/${id}/runs`),
  runTrace: (automationID: number, runID: number) => get<TraceMessage[]>(`/runs/${automationID}/${runID}/trace`),
  runStop: (runID: number) => post<{ stopped: boolean }>(`/runs/${runID}/stop`, {}),
  allRuns: (since?: string, limit?: number) => {
    const params = new URLSearchParams()
    if (since) params.set('since', since)
    if (limit) params.set('limit', String(limit))
    const q = params.toString()
    return get<AutomationRun[]>(`/runs${q ? '?' + q : ''}`)
  },
  skillCreate: (data: { name: string; description: string; parameters?: Record<string, unknown>; code: string }) =>
    post<{ name: string; success: boolean; tier: number }>('/skills/create', data),
  skillGet: (name: string) => get<{ name: string; description: string; code: string; tier: number }>(`/skills/${name}`),
  skillUpdate: (name: string, data: { description?: string; code?: string }) =>
    put<{ success: boolean; name: string }>(`/skills/${name}`, data),
  configKeys: () => get<ProviderKeys>('/config/keys'),
  configKeysSave: async (keys: ProviderKeys) => {
    const res = await fetch(BASE + '/config/keys', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(keys),
    })
    if (!res.ok) throw new Error(`API error ${res.status}: ${await res.text()}`)
  },

  chat: (messages: LLMMessage[], sessionId?: string) =>
    post<ChatResponse>('/chat', { messages, session_id: sessionId }),

  chatStream(
    messages: LLMMessage[],
    sessionId: string | undefined,
    onChunk: (chunk: StreamChunk) => void,
    signal?: AbortSignal
  ): Promise<void> {
    return fetch(BASE + '/chat/stream', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ messages, session_id: sessionId }),
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
