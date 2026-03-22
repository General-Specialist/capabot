import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { api, type Conversation, type Message } from '@/lib/api'

function MessageBubble({ msg }: { msg: Message }) {
  const isUser = msg.role === 'user'
  const isTool = msg.role === 'tool'

  if (isTool) {
    return (
      <div className="flex justify-center">
        <span
          className="text-xs px-3 py-1 rounded-full border font-mono"
          style={{
            borderColor: 'var(--color-border-white)',
            color: 'var(--color-dark-text-normal)',
            background: 'var(--color-sidebar-white)',
          }}
        >
          {msg.tool_name ?? 'tool'}: {msg.content.slice(0, 80)}{msg.content.length > 80 ? '…' : ''}
        </span>
      </div>
    )
  }

  return (
    <div className={`flex flex-col ${isUser ? 'items-end' : 'items-start'}`}>
      <div
        className="max-w-[80%] px-3 py-2 rounded-lg text-sm"
        style={
          isUser
            ? { background: 'var(--color-brand-primary)', color: '#ffffff' }
            : { background: 'var(--color-sidebar-hover-white)', color: 'var(--color-text-hover-black)' }
        }
      >
        <p className="whitespace-pre-wrap break-words leading-relaxed">{msg.content}</p>
      </div>
      <p className="text-xs mt-0.5 px-1" style={{ color: 'var(--color-dark-text-normal)' }}>
        {msg.token_count > 0 ? `${msg.token_count} tokens` : ''}
      </p>
    </div>
  )
}

export function ConversationDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [session, setSession] = useState<Conversation | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    api.conversation(id)
      .then(data => {
        if (!cancelled) {
          setSession(data.session)
          setMessages(data.messages)
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load conversation')
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [id])

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div
        className="flex items-center gap-3 px-4 h-14 border-b shrink-0"
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        <button
          onClick={() => navigate('/conversations')}
          className="p-1 rounded hover:bg-[var(--color-sidebar-hover-white)] transition-colors"
          style={{ color: 'var(--color-dark-text-normal)' }}
        >
          <ArrowLeft size={16} />
        </button>
        <div className="flex-1 min-w-0">
          {loading ? (
            <div className="h-4 w-40 rounded animate-pulse" style={{ background: 'var(--color-border-white)' }} />
          ) : session ? (
            <>
              <p className="text-sm font-medium truncate" style={{ color: 'var(--color-text-hover-black)' }}>
                {session.channel.length > 40 ? `${session.channel.slice(0, 40)}…` : session.channel}
              </p>
              <p className="text-xs" style={{ color: 'var(--color-dark-text-normal)' }}>
                {session.user_id} &middot; {messages.length} messages
              </p>
            </>
          ) : null}
        </div>
      </div>

      {/* Error */}
      {error && (
        <div
          className="px-4 py-2 text-xs border-b"
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
        {loading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <div
                key={i}
                className={`h-12 rounded-lg w-3/4 animate-pulse ${i % 2 === 0 ? 'ml-auto' : ''}`}
                style={{ background: 'var(--color-sidebar-hover-white)' }}
              />
            ))}
          </div>
        ) : messages.length === 0 ? (
          <div className="flex items-center justify-center h-full">
            <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
              No messages in this conversation.
            </p>
          </div>
        ) : (
          messages.map(msg => <MessageBubble key={msg.id} msg={msg} />)
        )}
      </div>
    </div>
  )
}
