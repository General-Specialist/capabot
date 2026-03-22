import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Conversation } from '@/lib/api'

function relativeTime(dateStr: string): string {
  const diff = (Date.now() - new Date(dateStr).getTime()) / 1000
  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  if (diff < 60) return rtf.format(-Math.round(diff), 'second')
  if (diff < 3600) return rtf.format(-Math.round(diff / 60), 'minute')
  if (diff < 86400) return rtf.format(-Math.round(diff / 3600), 'hour')
  if (diff < 2592000) return rtf.format(-Math.round(diff / 86400), 'day')
  return rtf.format(-Math.round(diff / 2592000), 'month')
}

export function ConversationsPage() {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    let cancelled = false
    api.conversations()
      .then(data => { if (!cancelled) setConversations(data) })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load conversations')
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  return (
    <div className="p-6 max-w-3xl">
      <h1 className="text-xl font-semibold mb-1" style={{ color: 'var(--color-text-hover-black)' }}>
        Conversations
      </h1>
      <p className="text-sm mb-6" style={{ color: 'var(--color-dark-text-normal)' }}>
        Recent chat sessions
      </p>

      {error && (
        <div
          className="mb-4 p-3 rounded-md text-sm border"
          style={{
            background: 'rgba(239,68,68,0.08)',
            borderColor: 'var(--color-red)',
            color: 'var(--color-red)',
          }}
        >
          {error}
        </div>
      )}

      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <div
              key={i}
              className="h-16 rounded-lg border animate-pulse"
              style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
            />
          ))}
        </div>
      ) : conversations.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
          No conversations yet.
        </p>
      ) : (
        <div className="space-y-2">
          {conversations.map(conv => (
            <button
              key={conv.id}
              onClick={() => navigate(`/conversations/${conv.id}`)}
              className="w-full text-left p-3 rounded-lg border transition-colors hover:bg-[var(--color-sidebar-hover-white)]"
              style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
            >
              <div className="flex items-start justify-between gap-3">
                <div className="flex-1 min-w-0">
                  <p className="font-mono text-xs truncate mb-0.5" style={{ color: 'var(--color-text-hover-black)' }}>
                    {conv.channel.length > 32 ? `${conv.channel.slice(0, 32)}…` : conv.channel}
                  </p>
                  <p className="text-xs" style={{ color: 'var(--color-dark-text-normal)' }}>
                    {conv.user_id} &middot; {relativeTime(conv.created_at)}
                  </p>
                </div>
                <span
                  className="shrink-0 text-xs px-2 py-0.5 rounded-full border"
                  style={{
                    borderColor: 'var(--color-border-white)',
                    color: 'var(--color-dark-text-normal)',
                  }}
                >
                  {conv.message_count} msgs
                </span>
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
