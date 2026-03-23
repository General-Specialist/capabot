import { useEffect, useRef, useState, useCallback } from 'react'
import { Send, Plus, History, Terminal, Globe, FileText, Search, FolderSearch, Pencil, Brain, CalendarClock, ListChecks, Image, FileCode, Wrench, ChevronRight, ChevronDown } from 'lucide-react'
import { api, type StreamChunk, type Conversation, type LLMMessage } from '@/lib/api'
import ReactMarkdown from 'react-markdown'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'

interface ToolCall {
  name: string
  label: string
  result?: string
}

interface Message {
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  toolCalls?: ToolCall[]
  usage?: { input_tokens: number; output_tokens: number }
}

function toolLabel(name: string, input?: Record<string, unknown>): string {
  if (!input) return name
  if (name === 'shell_exec') {
    const cmds = input.commands
    if (Array.isArray(cmds) && cmds.length > 0) {
      const first = cmds[0] as Record<string, unknown>
      const firstLabel = [first.command, ...(Array.isArray(first.args) ? first.args : [])].join(' ')
      return cmds.length === 1 ? firstLabel : `${firstLabel} (+${cmds.length - 1} more)`
    }
    const parts = [input.command, ...(Array.isArray(input.args) ? input.args : [])].filter(Boolean)
    return parts.join(' ') || name
  }
  const vals = Object.values(input).filter(v => typeof v === 'string' && (v as string).length < 40)
  return vals.length > 0 ? `${name}: ${vals[0]}` : name
}

function relativeTime(dateStr: string): string {
  const diff = (Date.now() - new Date(dateStr).getTime()) / 1000
  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  if (diff < 60) return rtf.format(-Math.round(diff), 'second')
  if (diff < 3600) return rtf.format(-Math.round(diff / 60), 'minute')
  if (diff < 86400) return rtf.format(-Math.round(diff / 3600), 'hour')
  if (diff < 2592000) return rtf.format(-Math.round(diff / 86400), 'day')
  return rtf.format(-Math.round(diff / 2592000), 'month')
}

function HistoryPanel({ onClose, onLoad }: {
  onClose: () => void
  onLoad: (messages: Message[], sessionId: string) => void
}) {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.conversations().then(setConversations).finally(() => setLoading(false))
  }, [])

  const filtered = query.trim()
    ? conversations.filter(c => c.channel.toLowerCase().includes(query.toLowerCase()))
    : conversations

  const load = async (c: Conversation) => {
    const { messages } = await api.conversation(c.id)
    // Reconstruct tool calls: tool messages with a tool_name get attached
    // to the assistant message that preceded them.
    const out: Message[] = []
    for (const m of messages) {
      if (m.role === 'user') {
        out.push({ role: 'user', content: m.content })
      } else if (m.role === 'assistant') {
        out.push({ role: 'assistant', content: m.content, toolCalls: [] })
      } else if (m.tool_name) {
        // Attach to most recent assistant message
        const last = out[out.length - 1]
        if (last?.role === 'assistant') {
          let input: Record<string, unknown> | undefined
          if (m.tool_input) try { input = JSON.parse(m.tool_input) } catch { /* ignore */ }
          const label = toolLabel(m.tool_name, input)
          last.toolCalls = [...(last.toolCalls ?? []), { name: m.tool_name, label, result: m.content }]
        }
      }
    }
    onLoad(out, c.id)
    onClose()
  }

  return (
    <>
      {/* backdrop */}
      <div className="fixed inset-0 z-40" onClick={onClose} />
      {/* panel */}
      <div className="absolute top-12 right-0 z-50 w-72 bg-white border border-border-white rounded-2xl shadow-lg flex flex-col overflow-hidden">
        <div className="p-3 border-b border-border-white">
          <input
            autoFocus
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Search conversations…"
            className="w-full text-sm px-3 py-1.5 rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none"
          />
        </div>
        <div className="overflow-y-auto max-h-80">
          {loading ? (
            <div className="p-3 space-y-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="h-10 rounded-lg animate-pulse bg-sidebar-hover-white" />
              ))}
            </div>
          ) : filtered.length === 0 ? (
            <p className="text-xs text-normal-black p-4">{query ? 'No matches.' : 'No conversations yet.'}</p>
          ) : (
            <div className="p-1.5 space-y-0.5">
              {filtered.map(c => (
                <button
                  key={c.id}
                  onClick={() => void load(c)}
                  className="w-full text-left px-3 py-2 rounded-lg hover:bg-sidebar-white transition-colors"
                >
                  <p className="text-sm text-hover-black truncate">{c.channel}</p>
                  <p className="text-xs text-normal-black">{c.message_count} msgs · {relativeTime(c.updated_at)}</p>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  )
}

const toolIcons: Record<string, typeof Terminal> = {
  shell_exec: Terminal,
  web_search: Globe,
  web_fetch: Globe,
  file_read: FileText,
  file_write: FileCode,
  file_edit: Pencil,
  glob: FolderSearch,
  grep: Search,
  memory_store: Brain,
  memory_recall: Brain,
  memory_delete: Brain,
  schedule: CalendarClock,
  todo: ListChecks,
  image_read: Image,
  pdf_read: FileText,
  notebook: FileCode,
  skill_create: Wrench,
}

function ToolCallChip({ tc }: { tc: ToolCall }) {
  const [open, setOpen] = useState(false)
  const Icon = toolIcons[tc.name] ?? Wrench
  const expandable = !!tc.result
  return (
    <div className="text-xs font-mono">
      <button
        onClick={() => expandable && setOpen(o => !o)}
        className={`flex items-center gap-1.5 truncate text-normal-black ${expandable ? 'cursor-pointer hover:text-hover-black' : 'cursor-default'}`}
      >
        <Icon size={12} className="shrink-0 opacity-60" />
        <span className="truncate">{tc.label}</span>
        {expandable && (open
          ? <ChevronDown size={10} className="shrink-0 opacity-40" />
          : <ChevronRight size={10} className="shrink-0 opacity-40" />
        )}
      </button>
      {open && tc.result && (
        <pre className="mt-1 ml-4.5 p-2 rounded-lg bg-sidebar-white text-normal-black text-xs overflow-x-auto max-h-48 overflow-y-auto whitespace-pre-wrap break-words">
          {tc.result}
        </pre>
      )}
    </div>
  )
}

export function ChatPage() {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showHistory, setShowHistory] = useState(false)
  const sessionId = useRef<string>(crypto.randomUUID())
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const toolCallsRef = useRef<ToolCall[]>([])
  // Full LLM-format history for round-tripping tool calls to the backend
  const llmHistoryRef = useRef<LLMMessage[]>([])
  const pendingToolCallsRef = useRef<{ id: string; name: string; input: unknown }[]>([])
  const pendingToolResultsRef = useRef<{ tool_use_id: string; content: string; is_error?: boolean }[]>([])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const updateLast = useCallback((patch: Partial<Message>) =>
    setMessages(prev => {
      const updated = [...prev]
      const last = updated[updated.length - 1]
      if (last?.role === 'assistant') updated[updated.length - 1] = { ...last, ...patch }
      return updated
    }), [])

  const send = useCallback(async (text: string) => {
    const trimmed = text.trim()
    if (!trimmed || loading) return
    setError(null)
    setLoading(true)
    abortRef.current?.abort()
    const ctrl = new AbortController()
    abortRef.current = ctrl
    toolCallsRef.current = []
    pendingToolCallsRef.current = []
    pendingToolResultsRef.current = []

    // Add user message to LLM history, build full history to send
    llmHistoryRef.current = [...llmHistoryRef.current, { role: 'user', content: trimmed }]
    const historyToSend = [...llmHistoryRef.current]

    setMessages(prev => [
      ...prev,
      { role: 'user', content: trimmed },
      { role: 'assistant', content: '', streaming: true, toolCalls: [] },
    ])
    let accumulated = ''
    try {
      await api.chatStream(historyToSend, sessionId.current, (chunk: StreamChunk) => {
        if (chunk.session_id) sessionId.current = chunk.session_id
        if (chunk.event === 'tool_start' && chunk.tool_name) {
          const tc: ToolCall = { name: chunk.tool_name, label: toolLabel(chunk.tool_name, chunk.tool_input) }
          toolCallsRef.current = [...toolCallsRef.current, tc]
          updateLast({ toolCalls: [...toolCallsRef.current] })
          pendingToolCallsRef.current = [
            ...pendingToolCallsRef.current,
            { id: chunk.tool_id ?? chunk.tool_name, name: chunk.tool_name, input: chunk.tool_input ?? {} },
          ]
        }
        if (chunk.event === 'tool_end' && chunk.tool_id) {
          pendingToolResultsRef.current = [
            ...pendingToolResultsRef.current,
            { tool_use_id: chunk.tool_id, content: chunk.content ?? '', is_error: chunk.is_error },
          ]
          // Nth tool_end matches Nth tool_start
          const idx = pendingToolResultsRef.current.length - 1
          if (idx < toolCallsRef.current.length) {
            const updated = [...toolCallsRef.current]
            updated[idx] = { ...updated[idx], result: chunk.content ?? '' }
            toolCallsRef.current = updated
            updateLast({ toolCalls: [...toolCallsRef.current] })
          }
        }
        if (chunk.event === 'response' && chunk.content) {
          accumulated = chunk.content
          updateLast({ content: accumulated, streaming: true })
        }
        if (chunk.done) {
          // Append assistant message (with any tool calls) + tool results to LLM history
          const assistantMsg: LLMMessage = { role: 'assistant', content: accumulated }
          if (pendingToolCallsRef.current.length > 0) {
            assistantMsg.tool_calls = pendingToolCallsRef.current
          }
          const toolMsgs: LLMMessage[] = pendingToolResultsRef.current.map(r => ({
            role: 'tool' as const,
            tool_result: r,
          }))
          llmHistoryRef.current = [...llmHistoryRef.current, assistantMsg, ...toolMsgs]
          pendingToolCallsRef.current = []
          pendingToolResultsRef.current = []
          updateLast({ content: accumulated, streaming: false, usage: chunk.usage })
          setLoading(false)
        }
        if (chunk.error) {
          updateLast({ content: accumulated || chunk.error, streaming: false })
          setError(chunk.error)
          setLoading(false)
        }
      }, ctrl.signal)
    } catch (err) {
      if ((err as { name?: string }).name === 'AbortError') { setLoading(false); return }
      const msg = err instanceof Error ? err.message : 'Failed to connect'
      updateLast({ content: accumulated || msg, streaming: false })
      setError(msg)
      setLoading(false)
    }
  }, [loading, updateLast])

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    void send(input)
    setInput('')
    if (textareaRef.current) textareaRef.current.style.height = 'auto'
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void send(input)
      setInput('')
      if (textareaRef.current) textareaRef.current.style.height = 'auto'
    }
  }

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value)
    const el = e.target
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`
  }

  const newChat = () => {
    abortRef.current?.abort()
    sessionId.current = crypto.randomUUID()
    setMessages([])
    setError(null)
    setLoading(false)
    setInput('')
    llmHistoryRef.current = []
    pendingToolCallsRef.current = []
    pendingToolResultsRef.current = []
  }

  const loadHistory = (msgs: Message[], sid: string) => {
    sessionId.current = sid
    setMessages(msgs)
    setError(null)
    // Reconstruct text-only LLM history (tool call round-trip not preserved for past sessions)
    llmHistoryRef.current = msgs
      .filter(m => !m.streaming)
      .map(m => ({ role: m.role as LLMMessage['role'], content: m.content }))
    pendingToolCallsRef.current = []
    pendingToolResultsRef.current = []
  }

  const hasMessages = messages.length > 0

  return (
    <div className="w-full h-screen flex flex-col bg-white">

      <div className="flex items-center justify-between px-6 h-12 shrink-0 relative">
        {hasMessages && (
          <span className="text-sm font-medium text-hover-black">Chat</span>
        )}
        <div className="ml-auto flex items-center gap-1.5">
          <div className="relative">
            <button
              onClick={() => setShowHistory(h => !h)}
              className="w-7 h-7 rounded-full flex items-center justify-center hover:bg-sidebar-hover-white transition-colors"
              title="History"
            >
              <History size={14} className="text-normal-black" />
            </button>
            {showHistory && (
              <HistoryPanel onClose={() => setShowHistory(false)} onLoad={loadHistory} />
            )}
          </div>
          {hasMessages && (
            <button
              onClick={newChat}
              className="w-7 h-7 rounded-full flex items-center justify-center hover:bg-sidebar-hover-white transition-colors"
              title="New chat"
            >
              <Plus size={14} className="text-hover-black" />
            </button>
          )}
        </div>
      </div>

      {error && (
        <div className="mx-6 mt-2 px-4 py-2 text-xs rounded-xl bg-red text-white shrink-0">{error}</div>
      )}

      {!hasMessages ? (
        <div className="flex-1 flex flex-col items-center justify-center px-6 gap-6">
          <p className="text-3xl font-medium text-hover-black text-center">What's on the agenda?</p>
          <InputBar
            ref={textareaRef}
            value={input}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            onSubmit={handleSubmit}
            loading={loading}
            className="w-full max-w-xl"
          />
        </div>
      ) : (
        <>
          <div className="flex-1 min-h-0 overflow-y-auto px-6 py-4 space-y-5 scrollbar-hide">
            <div className="max-w-3xl mx-auto space-y-5">
              {messages.map((msg, i) =>
                msg.role === 'user' ? (
                  <div key={i} className="flex justify-end">
                    <div className="max-w-[75%] px-5 py-3 rounded-3xl text-sm leading-relaxed bg-brand-primary text-white">
                      <p className="whitespace-pre-wrap break-words">{msg.content}</p>
                    </div>
                  </div>
                ) : (
                  <div key={i} className="flex flex-col items-start gap-1">
                    {(msg.toolCalls ?? []).length > 0 && (
                      <div className="flex flex-col gap-1 w-full max-w-[85%]">
                        {(msg.toolCalls ?? []).map((tc, j) => (
                          <ToolCallChip key={j} tc={tc} />
                        ))}
                      </div>
                    )}
                    {(msg.content || msg.streaming) && (
                      <div className="text-sm leading-relaxed text-hover-black max-w-[85%] prose prose-sm max-w-none [&_*]:text-inherit [&_p]:my-1 [&_pre]:bg-icon-white [&_pre]:rounded-lg [&_pre]:p-3 [&_code]:text-xs [&_p:last-child]:mb-0">
                        {msg.content ? (
                          <ReactMarkdown
                            remarkPlugins={[remarkMath]}
                            rehypePlugins={[rehypeKatex]}
                          >
                            {msg.content}
                          </ReactMarkdown>
                        ) : (
                          <span className="inline-block w-1.5 h-4 align-text-bottom animate-pulse rounded-sm bg-normal-black opacity-50" />
                        )}
                      </div>
                    )}
                    {!msg.streaming && msg.usage && (
                      <p className="text-xs text-normal-black opacity-60">
                        {msg.usage.input_tokens}↑ {msg.usage.output_tokens}↓
                      </p>
                    )}
                  </div>
                )
              )}
              <div ref={bottomRef} />
            </div>
          </div>

          <div className="px-6 py-4 shrink-0 border-t border-border-white">
            <div className="max-w-3xl mx-auto">
              <InputBar
                ref={textareaRef}
                value={input}
                onChange={handleChange}
                onKeyDown={handleKeyDown}
                onSubmit={handleSubmit}
                loading={loading}
              />
            </div>
          </div>
        </>
      )}
    </div>
  )
}

const InputBar = ({
  ref,
  value,
  onChange,
  onKeyDown,
  onSubmit,
  loading,
  className,
}: {
  ref: React.RefObject<HTMLTextAreaElement | null>
  value: string
  onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onSubmit: (e: React.FormEvent) => void
  loading: boolean
  className?: string
}) => (
  <form
    onSubmit={onSubmit}
    className={`flex items-center gap-3 rounded-full border border-border-white bg-sidebar-white px-4 py-2.5 ${className ?? ''}`}
  >
    <textarea
      ref={ref}
      value={value}
      onChange={onChange}
      onKeyDown={onKeyDown}
      placeholder="Ask anything"
      rows={1}
      disabled={loading}
      className="flex-1 resize-none text-sm leading-relaxed bg-transparent outline-none text-hover-black disabled:opacity-50"
      style={{ maxHeight: 160 }}
    />
    <button
      type="submit"
      disabled={loading || !value.trim()}
      className="shrink-0 h-8 w-8 rounded-full flex items-center justify-center bg-brand-primary disabled:opacity-30"
    >
      <Send size={13} className="text-white" />
    </button>
  </form>
)
