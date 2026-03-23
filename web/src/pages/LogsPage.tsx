import { useEffect, useRef, useState } from 'react'

interface LogLine { id: number; text: string; level: 'info' | 'warn' | 'error' | 'debug' | 'trace' | 'unknown' }

function detectLevel(line: string): LogLine['level'] {
  const l = line.toLowerCase()
  if (l.includes('"level":"error"') || l.includes(' ERR ')) return 'error'
  if (l.includes('"level":"warn"') || l.includes(' WRN ')) return 'warn'
  if (l.includes('"level":"debug"') || l.includes(' DBG ')) return 'debug'
  if (l.includes('"level":"trace"') || l.includes(' TRC ')) return 'trace'
  if (l.includes('"level":"info"') || l.includes(' INF ')) return 'info'
  return 'unknown'
}

const levelColor: Record<LogLine['level'], string> = {
  error: 'text-red', warn: 'text-terminal-yellow', info: 'text-terminal-green',
  debug: 'text-terminal-blue', trace: 'text-normal-black', unknown: 'text-normal-black',
}

export function LogsPage() {
  const [lines, setLines] = useState<LogLine[]>([])
  const [connected, setConnected] = useState(false)
  const [filter, setFilter] = useState('')
  const counter = useRef(0)
  const bottomRef = useRef<HTMLDivElement>(null)
  const autoScroll = useRef(true)

  useEffect(() => {
    let aborted = false
    let buf = ''
    const ctrl = new AbortController()

    fetch('/api/logs', { signal: ctrl.signal }).then(async res => {
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
              if (payload.line) {
                const id = ++counter.current
                setLines(prev => {
                  const next = [...prev, { id, text: payload.line!, level: detectLevel(payload.line!) }]
                  return next.length > 2000 ? next.slice(-2000) : next
                })
              }
            } catch { /* ignore */ }
          }
        }
      }
    }).catch(() => {}).finally(() => setConnected(false))

    return () => { aborted = true; ctrl.abort() }
  }, [])

  useEffect(() => {
    if (autoScroll.current) bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [lines])

  const filtered = filter ? lines.filter(l => l.text.toLowerCase().includes(filter.toLowerCase())) : lines

  return (
    <div className="w-full h-screen flex flex-col bg-white">
      <div className="flex items-center justify-between px-6 h-12 border-b border-border-white shrink-0">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold text-hover-black">Logs</span>
          <span className={`text-xs px-2 py-0.5 rounded-full font-mono ${connected ? 'bg-terminal-green text-white' : 'bg-red text-white'}`}>
            {connected ? 'live' : 'off'}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <input
            value={filter}
            onChange={e => setFilter(e.target.value)}
            placeholder="Filter…"
            className="text-xs px-2 py-1 rounded border border-border-white bg-sidebar-white text-hover-black font-mono w-36"
          />
          <button
            onClick={() => setLines([])}
            className="text-xs px-2 py-1 rounded border border-border-white bg-sidebar-white text-normal-black hover:bg-sidebar-hover-white transition-colors"
          >
            Clear
          </button>
        </div>
      </div>

      <div
        className="flex-1 min-h-0 overflow-y-auto p-4 font-mono text-xs scrollbar-hide"
        onScroll={e => {
          const el = e.currentTarget
          autoScroll.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60
        }}
      >
        {filtered.length === 0 && (
          <p className="text-center py-8 text-normal-black">
            {connected ? 'Waiting for logs…' : 'Connecting…'}
          </p>
        )}
        {filtered.map(line => (
          <div key={line.id} className={`leading-5 whitespace-pre-wrap break-all py-px ${levelColor[line.level]}`}>
            {line.text}
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
