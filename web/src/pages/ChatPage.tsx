import { useEffect, useRef, useState, useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Send, RotateCcw } from 'lucide-react'
import { api, type StreamChunk } from '@/lib/api'
import { cn } from '@/lib/utils'

interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  toolCalls?: string[]
  usage?: { input_tokens: number; output_tokens: number }
  iterations?: number
}

function MessageBubble({ msg }: { msg: ChatMessage }) {
  const isUser = msg.role === 'user'
  return (
    <div className={cn('flex flex-col', isUser ? 'items-end' : 'items-start')}>
      {(msg.toolCalls ?? []).length > 0 && (
        <div className="flex flex-wrap gap-1 mb-1 px-1">
          {(msg.toolCalls ?? []).map((name, i) => (
            <span
              key={i}
              className="text-xs px-2 py-0.5 rounded-full border font-mono"
              style={{
                borderColor: 'var(--color-border-white)',
                color: 'var(--color-dark-text-normal)',
                background: 'var(--color-sidebar-white)',
              }}
            >
              {name}
            </span>
          ))}
        </div>
      )}
      <div
        className="max-w-[80%] px-3 py-2 rounded-lg text-sm"
        style={
          isUser
            ? { background: 'var(--color-brand-primary)', color: '#ffffff' }
            : { background: 'var(--color-sidebar-hover-white)', color: 'var(--color-text-hover-black)' }
        }
      >
        <p className="whitespace-pre-wrap break-words leading-relaxed">{msg.content}</p>
        {msg.streaming && (
          <span
            className="inline-block w-2 h-3.5 ml-0.5 align-text-bottom animate-pulse rounded-sm"
            style={{ background: 'currentColor', opacity: 0.7 }}
          />
        )}
      </div>
      {!msg.streaming && msg.usage && (
        <p className="text-xs mt-0.5 px-1" style={{ color: 'var(--color-dark-text-normal)' }}>
          {msg.usage.input_tokens}↑ {msg.usage.output_tokens}↓ tokens
          {msg.iterations !== undefined && msg.iterations > 0 ? ` · ${msg.iterations} iter` : ''}
        </p>
      )}
    </div>
  )
}

export function ChatPage() {
  const [searchParams] = useSearchParams()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const sessionId = useRef<string>(crypto.randomUUID())
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const initialQuerySent = useRef(false)

  const scrollToBottom = useCallback(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [])

  useEffect(() => {
    scrollToBottom()
  }, [messages, scrollToBottom])

  const sendMessage = useCallback(async (text: string) => {
    const trimmed = text.trim()
    if (!trimmed || loading) return

    setError(null)
    setLoading(true)

    // Cancel any ongoing stream
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    setMessages(prev => [
      ...prev,
      { role: 'user', content: trimmed },
      { role: 'assistant', content: '', streaming: true, toolCalls: [] },
    ])

    let accumulated = ''
    const toolCallNames: string[] = []

    try {
      await api.chatStream(
        trimmed,
        sessionId.current,
        (chunk: StreamChunk) => {
          if (chunk.error) {
            setMessages(prev => {
              const updated = [...prev]
              const last = updated[updated.length - 1]
              if (last?.role === 'assistant') {
                updated[updated.length - 1] = {
                  ...last,
                  content: accumulated || 'An error occurred.',
                  streaming: false,
                }
              }
              return updated
            })
            setError(chunk.error ?? 'Stream error')
            return
          }

          if (chunk.tool_call) {
            toolCallNames.push(chunk.tool_call.name)
            setMessages(prev => {
              const updated = [...prev]
              const last = updated[updated.length - 1]
              if (last?.role === 'assistant') {
                updated[updated.length - 1] = {
                  ...last,
                  toolCalls: [...toolCallNames],
                }
              }
              return updated
            })
          }

          if (chunk.delta) {
            accumulated += chunk.delta
            setMessages(prev => {
              const updated = [...prev]
              const last = updated[updated.length - 1]
              if (last?.role === 'assistant') {
                updated[updated.length - 1] = {
                  ...last,
                  content: accumulated,
                  streaming: true,
                }
              }
              return updated
            })
          }

          if (chunk.done) {
            setMessages(prev => {
              const updated = [...prev]
              const last = updated[updated.length - 1]
              if (last?.role === 'assistant') {
                updated[updated.length - 1] = {
                  ...last,
                  content: accumulated,
                  streaming: false,
                }
              }
              return updated
            })
            setLoading(false)
          }
        },
        controller.signal
      )
    } catch (err: unknown) {
      if ((err as { name?: string }).name === 'AbortError') {
        setLoading(false)
        return
      }
      const msg = err instanceof Error ? err.message : 'Failed to connect to API'
      setMessages(prev => {
        const updated = [...prev]
        const last = updated[updated.length - 1]
        if (last?.role === 'assistant') {
          updated[updated.length - 1] = {
            ...last,
            content: accumulated || msg,
            streaming: false,
          }
        }
        return updated
      })
      setError(msg)
    } finally {
      setLoading(false)
    }
  }, [loading])

  // Handle initial query from URL
  useEffect(() => {
    const q = searchParams.get('q')
    if (q && !initialQuerySent.current) {
      initialQuerySent.current = true
      void sendMessage(q)
    }
  }, [searchParams, sendMessage])

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    void sendMessage(input)
    setInput('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      void sendMessage(input)
      setInput('')
      if (textareaRef.current) {
        textareaRef.current.style.height = 'auto'
      }
    }
  }

  const handleTextareaChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value)
    const el = e.target
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }

  const handleNewChat = () => {
    abortRef.current?.abort()
    sessionId.current = crypto.randomUUID()
    setMessages([])
    setError(null)
    setLoading(false)
    setInput('')
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div
        className="flex items-center justify-between px-4 h-14 border-b shrink-0"
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        <h1 className="text-sm font-semibold" style={{ color: 'var(--color-text-hover-black)' }}>
          Chat
        </h1>
        <button
          onClick={handleNewChat}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border transition-colors hover:bg-[var(--color-sidebar-hover-white)]"
          style={{
            borderColor: 'var(--color-border-white)',
            color: 'var(--color-dark-text-normal)',
          }}
        >
          <RotateCcw size={12} />
          New chat
        </button>
      </div>

      {/* Error banner */}
      {error && (
        <div
          className="px-4 py-2 text-xs border-b shrink-0"
          style={{
            background: 'rgba(239,68,68,0.08)',
            borderColor: 'rgba(239,68,68,0.3)',
            color: 'var(--color-red)',
          }}
        >
          {error}
        </div>
      )}

      {/* Messages */}
      <div className="flex-1 overflow-y-auto px-4 py-4 space-y-3 scrollbar-hide">
        {messages.length === 0 && (
          <div className="flex items-center justify-center h-full">
            <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
              Send a message to start chatting
            </p>
          </div>
        )}
        {messages.map((msg, i) => (
          <MessageBubble key={i} msg={msg} />
        ))}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div
        className="px-4 py-3 border-t shrink-0"
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        <form onSubmit={handleSubmit} className="flex items-end gap-2">
          <textarea
            ref={textareaRef}
            value={input}
            onChange={handleTextareaChange}
            onKeyDown={handleKeyDown}
            placeholder="Message... (Ctrl+Enter to send)"
            rows={1}
            disabled={loading}
            className="flex-1 resize-none px-3 py-2 rounded-lg border text-sm leading-relaxed disabled:opacity-50"
            style={{
              background: 'var(--color-sidebar-white)',
              borderColor: 'var(--color-border-white)',
              color: 'var(--color-text-hover-black)',
              maxHeight: '200px',
              outline: 'none',
            }}
          />
          <button
            type="submit"
            disabled={loading || !input.trim()}
            className="shrink-0 h-9 w-9 rounded-lg flex items-center justify-center transition-opacity disabled:opacity-40"
            style={{ background: 'var(--color-brand-primary)', color: '#ffffff' }}
          >
            <Send size={14} />
          </button>
        </form>
        <p className="text-xs mt-1.5" style={{ color: 'var(--color-dark-text-normal)' }}>
          Session: <span className="font-mono">{sessionId.current.slice(0, 8)}</span>
        </p>
      </div>
    </div>
  )
}
