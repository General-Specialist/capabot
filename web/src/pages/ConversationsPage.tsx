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
      .catch((err: unknown) => { if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6 max-w-3xl">
      <h1 className="text-lg font-semibold text-hover-black mb-4">Conversations</h1>

      {error && <p className="text-sm text-red mb-4">{error}</p>}

      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="h-14 rounded-lg animate-pulse bg-sidebar-hover-white" />
          ))}
        </div>
      ) : conversations.length === 0 ? (
        <p className="text-sm text-normal-black">No conversations yet.</p>
      ) : (
        <div className="space-y-1">
          {conversations.map(c => (
            <button
              key={c.id}
              onClick={() => navigate(`/conversations/${c.id}`)}
              className="w-full text-left px-4 py-3 rounded-lg hover:bg-sidebar-white transition-colors"
            >
              <p className="text-sm font-medium text-hover-black truncate">{c.channel}</p>
              <p className="text-xs text-normal-black mt-0.5">
                {c.message_count} messages · {relativeTime(c.updated_at)}
              </p>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
