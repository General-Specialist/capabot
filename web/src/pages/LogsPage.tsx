import { useEffect, useRef, useState } from 'react'
import { Terminal, Trash2 } from 'lucide-react'

interface LogLine {
  id: number
  text: string
  level: 'info' | 'warn' | 'error' | 'debug' | 'trace' | 'unknown'
}

function detectLevel(line: string): LogLine['level'] {
  const lower = line.toLowerCase()
  if (lower.includes('"level":"error"') || lower.includes(' ERR ')) return 'error'
  if (lower.includes('"level":"warn"') || lower.includes(' WRN ')) return 'warn'
  if (lower.includes('"level":"debug"') || lower.includes(' DBG ')) return 'debug'
  if (lower.includes('"level":"trace"') || lower.includes(' TRC ')) return 'trace'
  if (lower.includes('"level":"info"') || lower.includes(' INF ')) return 'info'
  return 'unknown'
}

const levelColors: Record<LogLine['level'], string> = {
  error: 'var(--color-red)',
  warn: 'var(--color-terminal-yellow)',
  info: 'var(--color-terminal-green)',
  debug: 'var(--color-terminal-blue)',
  trace: 'var(--color-dark-text-normal)',
  unknown: 'var(--color-dark-text-normal)',
}

export function LogsPage() {
  const [lines, setLines] = useState<LogLine[]>([])
  const [filter, setFilter] = useState('')
  const [connected, setConnected] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const counterRef = useRef(0)
  const bottomRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const addLine = (text: string) => {
      const id = ++counterRef.current
      setLines(prev => {
        const next = [...prev, { id, text, level: detectLevel(text) }]
        return next.length > 2000 ? next.slice(-2000) : next
      })
    }

    let buf = ''
    let aborted = false
    const controller = new AbortController()

    fetch('/api/logs', { signal: controller.signal })
      .then(async res => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        setConnected(true)
        const reader = res.body!.getReader()
        const decoder = new TextDecoder()
        while (!aborted) {
          const { done, value } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })
          const parts = buf.split('\n')
          buf = parts.pop() ?? ''
          for (const part of parts) {
            if (part.startsWith('data: ')) {
              try {
                const payload = JSON.parse(part.slice(6)) as { line?: string }
                if (payload.line) addLine(payload.line)
              } catch {
                // ignore malformed lines
              }
            }
          }
        }
      })
      .catch(() => {})
      .finally(() => setConnected(false))

    return () => {
      aborted = true
      controller.abort()
    }
  }, [])

  useEffect(() => {
    if (autoScroll) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [lines, autoScroll])

  const handleScroll = () => {
    const el = containerRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 60
    setAutoScroll(atBottom)
  }

  const filtered = filter
    ? lines.filter(l => l.text.toLowerCase().includes(filter.toLowerCase()))
    : lines

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div
        className="flex items-center justify-between px-4 h-14 border-b shrink-0"
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        <div className="flex items-center gap-2">
          <Terminal size={15} style={{ color: 'var(--color-brand-primary)' }} />
          <h1 className="text-sm font-semibold" style={{ color: 'var(--color-text-hover-black)' }}>
            Logs
          </h1>
          <span
            className="text-xs px-1.5 py-0.5 rounded-full font-mono"
            style={{
              background: connected ? 'rgba(88,204,2,0.12)' : 'rgba(239,68,68,0.1)',
              color: connected ? 'var(--color-terminal-green)' : 'var(--color-red)',
            }}
          >
            {connected ? 'live' : 'disconnected'}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <input
            type="text"
            value={filter}
            onChange={e => setFilter(e.target.value)}
            placeholder="Filter..."
            className="text-xs px-2 py-1 rounded border font-mono"
            style={{
              background: 'var(--color-sidebar-white)',
              borderColor: 'var(--color-border-white)',
              color: 'var(--color-text-hover-black)',
              outline: 'none',
              width: '160px',
            }}
          />
          <button
            onClick={() => setLines([])}
            className="p-1.5 rounded hover:bg-[var(--color-sidebar-hover-white)] transition-colors"
            style={{ color: 'var(--color-dark-text-normal)' }}
            title="Clear logs"
          >
            <Trash2 size={13} />
          </button>
        </div>
      </div>

      {/* Log output */}
      <div
        ref={containerRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto p-3 scrollbar-hide font-mono text-xs"
        style={{ background: 'var(--color-white)' }}
      >
        {filtered.length === 0 && (
          <p className="text-center py-8" style={{ color: 'var(--color-dark-text-normal)' }}>
            {connected ? 'Waiting for log entries...' : 'Connecting to log stream...'}
          </p>
        )}
        {filtered.map(line => (
          <div
            key={line.id}
            className="leading-5 whitespace-pre-wrap break-all py-px"
            style={{ color: levelColors[line.level] }}
          >
            {line.text}
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      {/* Auto-scroll indicator */}
      {!autoScroll && (
        <button
          onClick={() => {
            setAutoScroll(true)
            bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
          }}
          className="absolute bottom-6 right-6 text-xs px-3 py-1.5 rounded-full border transition-colors"
          style={{
            background: 'var(--color-sidebar-white)',
            borderColor: 'var(--color-border-white)',
            color: 'var(--color-dark-text-normal)',
          }}
        >
          ↓ scroll to bottom
        </button>
      )}
    </div>
  )
}
