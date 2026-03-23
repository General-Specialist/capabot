import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { api, type Conversation, type Message } from '@/lib/api'

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
        if (!cancelled) { setSession(data.session); setMessages(data.messages) }
      })
      .catch((err: unknown) => { if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [id])

  return (
    <div className="w-full h-screen flex flex-col bg-white">
      <div className="flex items-center gap-3 px-6 h-12 border-b border-border-white shrink-0">
        <button
          onClick={() => navigate('/conversations')}
          className="w-7 h-7 rounded-full flex items-center justify-center hover:bg-sidebar-hover-white transition-colors"
        >
          <ArrowLeft size={14} className="text-hover-black" />
        </button>
        {session && (
          <p className="text-sm font-medium text-hover-black truncate">{session.channel}</p>
        )}
      </div>

      {error && <div className="px-6 py-2 text-xs text-red border-b border-border-white">{error}</div>}

      <div className="flex-1 min-h-0 overflow-y-auto px-6 py-4 scrollbar-hide">
        <div className="max-w-3xl mx-auto space-y-3">
          {loading ? (
            Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className={`h-12 rounded-lg animate-pulse bg-sidebar-hover-white w-3/4 ${i % 2 === 0 ? 'ml-auto' : ''}`} />
            ))
          ) : messages.length === 0 ? (
            <p className="text-sm text-normal-black text-center py-8">No messages.</p>
          ) : (
            messages.map(msg => {
              const isUser = msg.role === 'user'
              const isTool = msg.role === 'tool'
              if (isTool) return (
                <div key={msg.id} className="flex justify-center">
                  <span className="text-xs px-3 py-1 rounded-full border border-border-white text-normal-black bg-sidebar-white font-mono">
                    {msg.tool_name ?? 'tool'}: {msg.content.slice(0, 80)}{msg.content.length > 80 ? '…' : ''}
                  </span>
                </div>
              )
              return (
                <div key={msg.id} className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[80%] px-4 py-2.5 rounded-2xl text-sm leading-relaxed ${
                    isUser ? 'bg-brand-primary text-white' : 'bg-sidebar-white text-hover-black'
                  }`}>
                    <p className="whitespace-pre-wrap break-words">{msg.content}</p>
                  </div>
                </div>
              )
            })
          )}
        </div>
      </div>
    </div>
  )
}
