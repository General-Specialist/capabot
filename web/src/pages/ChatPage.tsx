import { useEffect, useRef, useState, useCallback } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { Send, Square, Plus, Terminal, Globe, FileText, Search, FolderSearch, Pencil, Brain, CalendarClock, ListChecks, Image, FileCode, Wrench, ChevronRight, ChevronDown, Lightbulb } from 'lucide-react'
import { api, type StreamChunk, type LLMMessage } from '@/lib/api'
import { Markdown } from '@/components/Markdown'

interface ToolCall {
  name: string
  label: string
  result?: string
}

interface Message {
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  streaming?: boolean
  toolCalls?: ToolCall[]
  usage?: { input_tokens: number; output_tokens: number }
  persona?: string
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

function ThinkingChip({ text, streaming }: { text: string; streaming?: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="text-xs font-mono">
      <button
        onClick={() => setOpen(o => !o)}
        className="flex items-center gap-1.5 text-normal-black cursor-pointer hover:text-hover-black"
      >
        <Lightbulb size={12} className={`shrink-0 opacity-60 ${streaming ? 'animate-pulse' : ''}`} />
        <span>Thinking</span>
        {open
          ? <ChevronDown size={10} className="shrink-0 opacity-40" />
          : <ChevronRight size={10} className="shrink-0 opacity-40" />
        }
      </button>
      {open && (
        <pre className="mt-1 ml-4.5 p-2 rounded-lg bg-sidebar-white text-normal-black text-xs overflow-x-auto max-h-48 overflow-y-auto whitespace-pre-wrap break-words">
          {text}
        </pre>
      )}
    </div>
  )
}

export function ChatPage() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [thinking, setThinking] = useState(false)
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

  // Load session from URL param
  useEffect(() => {
    const sid = searchParams.get('session')
    if (!sid) {
      // New chat
      sessionId.current = crypto.randomUUID()
      setMessages([])
      setError(null)
      setInput('')
      llmHistoryRef.current = []
      return
    }
    api.conversation(sid).then(({ messages: msgs }) => {
      const out: Message[] = []
      for (const m of msgs) {
        if (m.role === 'user') {
          out.push({ role: 'user', content: m.content })
        } else if (m.role === 'assistant') {
          out.push({ role: 'assistant', content: m.content, toolCalls: [] })
        } else if (m.tool_name) {
          const last = out[out.length - 1]
          if (last?.role === 'assistant') {
            let input: Record<string, unknown> | undefined
            if (m.tool_input) try { input = JSON.parse(m.tool_input) } catch { /* ignore */ }
            last.toolCalls = [...(last.toolCalls ?? []), { name: m.tool_name, label: toolLabel(m.tool_name, input), result: m.content }]
          }
        }
      }
      sessionId.current = sid
      setMessages(out)
      setError(null)
      llmHistoryRef.current = out.filter(m => !m.streaming).map(m => ({ role: m.role as LLMMessage['role'], content: m.content }))
    }).catch(() => navigate('/'))
  }, [searchParams, navigate])

  const updateLast = useCallback((patch: Partial<Message>) =>
    setMessages(prev => {
      const updated = [...prev]
      const last = updated[updated.length - 1]
      if (last?.role === 'assistant') updated[updated.length - 1] = { ...last, ...patch }
      return updated
    }), [])

  // updatePersonMessage updates or inserts an assistant message for a specific person.
  const updatePersonMessage = useCallback((persona: string, patch: Partial<Message>) =>
    setMessages(prev => {
      const updated = [...prev]
      // Find the existing assistant message for this persona.
      const idx = updated.findLastIndex(m => m.role === 'assistant' && m.persona === persona)
      if (idx >= 0) {
        updated[idx] = { ...updated[idx], ...patch }
      } else {
        // Insert a new assistant message for this persona.
        updated.push({ role: 'assistant', content: '', streaming: true, toolCalls: [], persona, ...patch })
      }
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
    setThinking(false)

    // Add user message to LLM history, build full history to send
    llmHistoryRef.current = [...llmHistoryRef.current, { role: 'user', content: trimmed }]
    const historyToSend = [...llmHistoryRef.current]

    setMessages(prev => [
      ...prev,
      { role: 'user', content: trimmed },
    ])

    // Per-persona accumulators for multi-agent streams.
    const personaState: Record<string, {
      content: string
      thinking: string
      toolCalls: ToolCall[]
      pendingToolCalls: { id: string; name: string; input: unknown }[]
      pendingToolResults: { tool_use_id: string; content: string; is_error?: boolean }[]
    }> = {}
    const getState = (p: string) => {
      if (!personaState[p]) {
        personaState[p] = { content: '', thinking: '', toolCalls: [], pendingToolCalls: [], pendingToolResults: [] }
      }
      return personaState[p]
    }

    // Track whether this is a single-agent (no persona field) or multi-agent stream.
    let isMulti = false
    let accumulated = ''
    let accumulatedThinking = ''
    // For single-agent, add the placeholder immediately.
    let placeholderAdded = false

    try {
      await api.chatStream(historyToSend, sessionId.current, (chunk: StreamChunk) => {
        if (chunk.session_id) sessionId.current = chunk.session_id

        // Detect multi-persona mode.
        if (chunk.persona && !isMulti) {
          isMulti = true
          // Remove single-agent placeholder if we added one.
          if (placeholderAdded) {
            setMessages(prev => prev.filter((m, i) => !(i === prev.length - 1 && m.role === 'assistant' && !m.persona)))
          }
        }

        if (isMulti && chunk.persona) {
          const ps = getState(chunk.persona)
          if (chunk.event === 'thinking' && chunk.thinking) {
            ps.thinking += chunk.thinking
            updatePersonMessage(chunk.persona, { thinking: ps.thinking, streaming: true })
          }
          if (chunk.event === 'tool_start' && chunk.tool_name) {
            const tc: ToolCall = { name: chunk.tool_name, label: toolLabel(chunk.tool_name, chunk.tool_input) }
            ps.toolCalls = [...ps.toolCalls, tc]
            ps.pendingToolCalls = [...ps.pendingToolCalls, { id: chunk.tool_id ?? chunk.tool_name, name: chunk.tool_name, input: chunk.tool_input ?? {} }]
            updatePersonMessage(chunk.persona, { toolCalls: [...ps.toolCalls], streaming: true })
          }
          if (chunk.event === 'tool_end' && chunk.tool_id) {
            ps.pendingToolResults = [...ps.pendingToolResults, { tool_use_id: chunk.tool_id, content: chunk.content ?? '', is_error: chunk.is_error }]
            const idx = ps.pendingToolResults.length - 1
            if (idx < ps.toolCalls.length) {
              ps.toolCalls = [...ps.toolCalls]
              ps.toolCalls[idx] = { ...ps.toolCalls[idx], result: chunk.content ?? '' }
              updatePersonMessage(chunk.persona, { toolCalls: [...ps.toolCalls], streaming: true })
            }
          }
          if (chunk.event === 'response' && chunk.content) {
            ps.content = chunk.content
            updatePersonMessage(chunk.persona, { content: ps.content, streaming: true })
          }
        } else if (!isMulti) {
          // Single-agent path (no persona).
          if (!placeholderAdded) {
            setMessages(prev => [...prev, { role: 'assistant', content: '', streaming: true, toolCalls: [] }])
            placeholderAdded = true
          }
          if (chunk.event === 'thinking') {
            setThinking(true)
            if (chunk.thinking) {
              accumulatedThinking += chunk.thinking
              updateLast({ thinking: accumulatedThinking })
            }
          }
          if (chunk.event === 'tool_start' && chunk.tool_name) {
            setThinking(false)
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
            const idx = pendingToolResultsRef.current.length - 1
            if (idx < toolCallsRef.current.length) {
              const updated = [...toolCallsRef.current]
              updated[idx] = { ...updated[idx], result: chunk.content ?? '' }
              toolCallsRef.current = updated
              updateLast({ toolCalls: [...toolCallsRef.current] })
            }
          }
          if (chunk.event === 'response' && chunk.content) {
            setThinking(false)
            accumulated = chunk.content
            updateLast({ content: accumulated, streaming: true })
          }
        }

        if (chunk.done) {
          if (isMulti) {
            // Finalize all persona messages.
            for (const [pName, ps] of Object.entries(personaState)) {
              updatePersonMessage(pName, { content: ps.content, streaming: false })
            }
            // Add combined responses to LLM history.
            for (const ps of Object.values(personaState)) {
              llmHistoryRef.current = [...llmHistoryRef.current, { role: 'assistant', content: ps.content }]
            }
          } else {
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
          }
          setThinking(false)
          setLoading(false)
        }
        if (chunk.error) {
          if (isMulti && chunk.persona) {
            updatePersonMessage(chunk.persona, { content: chunk.error, streaming: false })
          } else {
            updateLast({ content: accumulated || chunk.error, streaming: false })
          }
          setError(chunk.error)
          setLoading(false)
        }
      }, ctrl.signal)
    } catch (err) {
      if ((err as { name?: string }).name === 'AbortError') { setLoading(false); return }
      const msg = err instanceof Error ? err.message : 'Failed to connect'
      if (isMulti) {
        setError(msg)
      } else {
        updateLast({ content: accumulated || msg, streaming: false })
        setError(msg)
      }
      setLoading(false)
    }
  }, [loading, updateLast, updatePersonMessage])

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
    navigate('/')
  }

  const hasMessages = messages.length > 0

  return (
    <div className="w-full h-screen flex flex-col bg-white">

      <div className="flex items-center justify-between px-6 h-12 shrink-0 relative">
        {hasMessages && (
          <span className="text-sm font-medium text-hover-black">Chat</span>
        )}
        {hasMessages && (
          <div className="ml-auto">
            <button
              onClick={newChat}
              className="w-7 h-7 rounded-full flex items-center justify-center hover:bg-sidebar-hover-white transition-colors"
              title="New chat"
            >
              <Plus size={14} className="text-hover-black" />
            </button>
          </div>
        )}
      </div>

      {error && (
        <div className="mx-6 mt-2 px-4 py-2 text-xs rounded-capsule bg-red text-white shrink-0">{error}</div>
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
            onStop={() => { abortRef.current?.abort(); setLoading(false) }}
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
                    <div className="max-w-[75%] px-5 py-3 rounded-capsule text-sm leading-relaxed bg-brand-primary text-white">
                      <p className="whitespace-pre-wrap break-words">{msg.content}</p>
                    </div>
                  </div>
                ) : (
                  <div key={i} className="flex flex-col items-start gap-1">
                    {msg.persona && (
                      <span className="text-xs font-medium text-brand-primary px-1">{msg.persona}</span>
                    )}
                    {msg.thinking && (
                      <ThinkingChip text={msg.thinking} streaming={msg.streaming && thinking} />
                    )}
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
                          <Markdown>{msg.content}</Markdown>
                        ) : thinking && !msg.thinking ? (
                          <span className="flex items-center gap-1.5 text-normal-black opacity-70">
                            <Lightbulb size={13} className="animate-pulse" />
                            <span className="text-xs">Thinking…</span>
                          </span>
                        ) : !thinking ? (
                          <span className="inline-block w-1.5 h-4 align-text-bottom animate-pulse rounded-sm bg-normal-black opacity-50" />
                        ) : null}
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
                onStop={() => { abortRef.current?.abort(); setLoading(false) }}
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
  onStop,
  loading,
  className,
}: {
  ref: React.RefObject<HTMLTextAreaElement | null>
  value: string
  onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onSubmit: (e: React.FormEvent) => void
  onStop: () => void
  loading: boolean
  className?: string
}) => (
  <form
    onSubmit={onSubmit}
    className={`flex items-center gap-3 rounded-capsule border border-border-white bg-sidebar-white px-4 py-2.5 ${className ?? ''}`}
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
    {loading ? (
      <button
        type="button"
        onClick={onStop}
        className="shrink-0 h-8 w-8 rounded-full flex items-center justify-center bg-brand-primary"
      >
        <Square size={11} className="text-white fill-white" />
      </button>
    ) : (
      <button
        type="submit"
        disabled={!value.trim()}
        className="shrink-0 h-8 w-8 rounded-full flex items-center justify-center bg-brand-primary disabled:opacity-30"
      >
        <Send size={13} className="text-white" />
      </button>
    )}
  </form>
)
