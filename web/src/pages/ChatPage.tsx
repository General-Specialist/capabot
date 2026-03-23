import { useEffect, useRef, useState, useCallback } from 'react'
import { Send, Plus } from 'lucide-react'
import { api, type StreamChunk } from '@/lib/api'

interface ToolCall {
  name: string
  label: string // human-readable: actual command for shell_exec, tool name otherwise
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
    // Multi-command batch
    const cmds = input.commands
    if (Array.isArray(cmds) && cmds.length > 0) {
      const first = cmds[0] as Record<string, unknown>
      const firstLabel = [first.command, ...(Array.isArray(first.args) ? first.args : [])].join(' ')
      return cmds.length === 1 ? firstLabel : `${firstLabel} (+${cmds.length - 1} more)`
    }
    // Single command
    const parts = [input.command, ...(Array.isArray(input.args) ? input.args : [])].filter(Boolean)
    return parts.join(' ') || name
  }
  // For other tools, show name + first meaningful param value if short
  const vals = Object.values(input).filter(v => typeof v === 'string' && (v as string).length < 40)
  return vals.length > 0 ? `${name}: ${vals[0]}` : name
}

export function ChatPage() {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const sessionId = useRef<string>(crypto.randomUUID())
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const toolCallsRef = useRef<ToolCall[]>([])

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
    setMessages(prev => [
      ...prev,
      { role: 'user', content: trimmed },
      { role: 'assistant', content: '', streaming: true, toolCalls: [] },
    ])
    let accumulated = ''
    try {
      await api.chatStream(trimmed, sessionId.current, (chunk: StreamChunk) => {
        if (chunk.session_id) sessionId.current = chunk.session_id
        if (chunk.event === 'tool_start' && chunk.tool_name) {
          const tc: ToolCall = { name: chunk.tool_name, label: toolLabel(chunk.tool_name, chunk.tool_input) }
          toolCallsRef.current = [...toolCallsRef.current, tc]
          updateLast({ toolCalls: [...toolCallsRef.current] })
        }
        if (chunk.event === 'response' && chunk.content) {
          accumulated = chunk.content
          updateLast({ content: accumulated, streaming: true })
        }
        if (chunk.done) {
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
  }

  const hasMessages = messages.length > 0

  return (
    <div className="w-full h-screen flex flex-col bg-white">

      {hasMessages && (
        <div className="flex items-center justify-between px-6 h-12 border-b border-border-white shrink-0">
          <span className="text-sm font-medium text-hover-black">Chat</span>
          <button
            onClick={newChat}
            className="w-7 h-7 rounded-full flex items-center justify-center bg-sidebar-hover-white"
            title="New chat"
          >
            <Plus size={14} className="text-hover-black" />
          </button>
        </div>
      )}

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
                          <span key={j} className="text-xs px-2.5 py-1 rounded-lg border border-border-white text-normal-black font-mono bg-sidebar-white truncate">
                            {tc.label}
                          </span>
                        ))}
                      </div>
                    )}
                    <p className="text-sm leading-relaxed text-hover-black whitespace-pre-wrap break-words max-w-[85%]">
                      {msg.content}
                      {msg.streaming && (
                        <span className="inline-block w-1.5 h-4 ml-0.5 align-text-bottom animate-pulse rounded-sm bg-normal-black opacity-50" />
                      )}
                    </p>
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
