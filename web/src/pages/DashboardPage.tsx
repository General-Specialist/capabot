import { useEffect, useState } from 'react'
import { ChevronRight, Square } from 'lucide-react'
import { Markdown } from '@/components/Markdown'
import { api, type Automation, type AutomationRun, type TraceMessage } from '@/lib/api'

function formatTime(iso: string | null): string {
  if (!iso) return ''
  const d = new Date(iso.includes('T') ? iso : iso + 'Z')
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatDate(iso: string): string {
  const d = new Date(iso.includes('T') ? iso : iso + 'Z')
  return d.toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' })
}

function todayUTC(): string {
  const now = new Date()
  const y = now.getFullYear()
  const m = String(now.getMonth() + 1).padStart(2, '0')
  const d = String(now.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}T00:00:00Z`
}

function AgentTrace({ messages }: { messages: TraceMessage[] }) {
  const [openTool, setOpenTool] = useState<number | null>(null)
  // Skip first user message (it's just the automation prompt) and last assistant (shown as response)
  const steps = messages.filter((_, i) => i > 0 && i < messages.length - 1)
  if (steps.length === 0) return null

  return (
    <div className="space-y-1 mb-3">
      <p className="text-xs text-normal-black mb-1.5">Agent trace</p>
      {steps.map(msg => {
        if (msg.role === 'assistant' && msg.content) {
          return (
            <div key={msg.id} className="text-xs text-normal-black px-3 py-1 italic">
              {msg.content.length > 200 ? msg.content.slice(0, 200) + '...' : msg.content}
            </div>
          )
        }
        if (msg.role === 'tool') {
          const failed = msg.content.startsWith('browser error:') || msg.content.startsWith('exit code: -1') || msg.content.includes('not in allowlist')
          return (
            <div key={msg.id} className="rounded-lg border border-border-white text-xs">
              <button
                type="button"
                onClick={() => setOpenTool(o => o === msg.id ? null : msg.id)}
                className="w-full text-left flex items-center gap-2 px-3 py-1.5"
              >
                <span className={`shrink-0 w-1.5 h-1.5 rounded-full ${failed ? 'bg-terminal-red' : 'bg-terminal-green'}`} />
                <span className="font-mono text-hover-black">{msg.tool_name || 'tool'}</span>
                {msg.tool_input && <span className="text-normal-black truncate max-w-48">{msg.tool_input}</span>}
                <ChevronRight size={11} className={`ml-auto text-normal-black shrink-0 transition-transform ${openTool === msg.id ? 'rotate-90' : ''}`} />
              </button>
              {openTool === msg.id && (
                <div className="border-t border-border-white px-3 py-2">
                  <pre className="text-hover-black bg-icon-white rounded p-2 text-xs overflow-x-auto whitespace-pre-wrap max-h-48 overflow-y-auto">{msg.content}</pre>
                </div>
              )}
            </div>
          )
        }
        return null
      })}
    </div>
  )
}

interface LiveEvent {
  event?: string
  tool_name?: string
  tool_input?: string
  content?: string
  is_error?: boolean
  done?: boolean
}

function LiveStream({ runID, onDone }: { runID: number; onDone?: () => void }) {
  const [events, setEvents] = useState<LiveEvent[]>([])

  useEffect(() => {
    const es = new EventSource(`/api/runs/${runID}/stream`)
    es.onmessage = (e) => {
      const data = JSON.parse(e.data) as LiveEvent
      if (data.done) { es.close(); onDone?.(); return }
      setEvents(prev => [...prev, data])
    }
    es.onerror = () => { es.close(); onDone?.() }
    return () => es.close()
  }, [runID])

  if (events.length === 0) {
    return <p className="text-xs text-normal-black italic animate-pulse">Waiting for agent...</p>
  }

  return (
    <div className="space-y-1 max-h-64 overflow-y-auto">
      {events.map((ev, i) => {
        if (ev.event === 'tool_start') {
          return (
            <div key={i} className="flex items-center gap-2 text-xs px-3 py-1">
              <span className="w-1.5 h-1.5 rounded-full bg-normal-black animate-pulse shrink-0" />
              <span className="font-mono text-hover-black">{ev.tool_name}</span>
              {ev.tool_input && <span className="text-normal-black truncate max-w-64">{String(ev.tool_input).slice(0, 80)}</span>}
            </div>
          )
        }
        if (ev.event === 'tool_end') {
          const failed = ev.is_error
          return (
            <div key={i} className="flex items-center gap-2 text-xs px-3 py-1">
              <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${failed ? 'bg-terminal-red' : 'bg-terminal-green'}`} />
              <span className="font-mono text-hover-black">{ev.tool_name}</span>
              {ev.content && <span className="text-normal-black truncate max-w-64">{ev.content.slice(0, 80)}</span>}
            </div>
          )
        }
        if (ev.event === 'response' && ev.content) {
          return (
            <div key={i} className="text-xs text-normal-black px-3 py-1 italic">
              {ev.content.slice(0, 200)}{ev.content.length > 200 ? '...' : ''}
            </div>
          )
        }
        return null
      })}
    </div>
  )
}

function RunCard({ run, automationName }: { run: AutomationRun; automationName: string }) {
  const [status, setStatus] = useState(run.status)
  const isRunning = status === 'running'
  const [expanded, setExpanded] = useState(isRunning)
  const [trace, setTrace] = useState<TraceMessage[] | null>(null)
  const body = run.error || run.response
  const isError = status === 'error'
  const hasContent = body || isRunning

  const toggle = () => {
    if (!hasContent && !isRunning) return
    const next = !expanded
    setExpanded(next)
    if (next && !isRunning && trace === null) {
      api.runTrace(run.automation_id, run.id).then(setTrace).catch(() => setTrace([]))
    }
  }

  return (
    <div className="border border-border-white rounded-xl">
      <button
        type="button"
        onClick={toggle}
        className={`w-full text-left flex items-center gap-3 px-4 py-3 ${hasContent ? 'cursor-pointer' : ''}`}
      >
        <span className={`shrink-0 w-2 h-2 rounded-full ${
          status === 'success' ? 'bg-terminal-green' :
          status === 'error' ? 'bg-terminal-red' :
          status === 'stopped' ? 'bg-terminal-yellow' : 'bg-normal-black animate-pulse'
        }`} />
        <span className="text-sm font-medium text-hover-black truncate">{automationName}</span>
        <span className="text-xs text-normal-black shrink-0">{formatTime(run.started_at)}</span>
        <span className={`text-xs font-medium shrink-0 ${
          isError ? 'text-terminal-red' : status === 'success' ? 'text-terminal-green' : status === 'stopped' ? 'text-terminal-yellow' : 'text-normal-black'
        }`}>{status}</span>
        {isRunning && (
          <button
            type="button"
            onClick={e => { e.stopPropagation(); api.runStop(run.id).then(() => setStatus('stopped')) }}
            className="ml-auto shrink-0 p-1 rounded hover:bg-border-white text-red"
            title="Stop run"
          >
            <Square size={12} fill="currentColor" />
          </button>
        )}
        {!isRunning && hasContent && <ChevronRight size={13} className={`ml-auto text-normal-black shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />}
      </button>
      {expanded && (
        <div className="border-t border-border-white px-4 py-3">
          {isRunning
            ? <LiveStream runID={run.id} onDone={() => {
                setStatus('success')
                api.runTrace(run.automation_id, run.id).then(setTrace).catch(() => setTrace([]))
              }} />
            : <>
                {trace && trace.length > 0 && <AgentTrace messages={trace} />}
                {body && (
                  isError
                    ? <p className="font-mono text-xs text-terminal-red whitespace-pre-wrap">{body}</p>
                    : (
                      <div className="text-sm leading-relaxed text-hover-black prose prose-sm max-w-none [&_*]:text-inherit [&_p]:my-1 [&_pre]:bg-icon-white [&_pre]:rounded-lg [&_pre]:p-3 [&_code]:text-xs [&_p:last-child]:mb-0">
                        <Markdown>{body}</Markdown>
                      </div>
                    )
                )}
              </>
          }
        </div>
      )}
    </div>
  )
}

export function DashboardPage() {
  const [todayRuns, setTodayRuns] = useState<AutomationRun[]>([])
  const [allRuns, setAllRuns] = useState<AutomationRun[]>([])
  const [automations, setAutomations] = useState<Automation[]>([])
  const [showAll, setShowAll] = useState(false)
  const [loading, setLoading] = useState(true)

  const nameMap = new Map(automations.map(a => [a.id, a.name]))

  useEffect(() => {
    Promise.all([
      api.allRuns(todayUTC(), 100),
      api.automations(),
    ]).then(([runs, autos]) => {
      setTodayRuns(runs)
      setAutomations(autos)
    }).catch(() => {}).finally(() => setLoading(false))
  }, [])

  const loadAll = () => {
    setShowAll(true)
    api.allRuns(undefined, 200).then(setAllRuns).catch(() => {})
  }

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        {!loading && todayRuns.length === 0 ? (
          <h2 className="text-sm font-medium text-hover-black mb-3">No runs today.</h2>
        ) : (
          <>
            <h2 className="text-sm font-medium text-hover-black mb-3">Today</h2>
            <div className="space-y-2">
              {todayRuns.map(run => (
                <RunCard key={run.id} run={run} automationName={nameMap.get(run.automation_id) || `#${run.automation_id}`} />
              ))}
            </div>
          </>
        )}

        {!showAll && (
          <button
            type="button"
            onClick={loadAll}
            className="mt-4 text-sm text-normal-black hover:opacity-70 transition-opacity"
          >
            Show all history
          </button>
        )}

        {showAll && allRuns.length > 0 && (() => {
          // group by date
          const groups = new Map<string, AutomationRun[]>()
          for (const run of allRuns) {
            const key = formatDate(run.started_at)
            const list = groups.get(key) || []
            list.push(run)
            groups.set(key, list)
          }
          // skip today since it's shown above
          const todayKey = formatDate(new Date().toISOString())

          return (
            <div className="mt-6 space-y-5">
              {[...groups.entries()]
                .filter(([date]) => date !== todayKey)
                .map(([date, runs]) => (
                <div key={date}>
                  <h2 className="text-sm font-medium text-normal-black mb-3">{date}</h2>
                  <div className="space-y-2">
                    {runs.map(run => (
                      <RunCard key={run.id} run={run} automationName={nameMap.get(run.automation_id) || `#${run.automation_id}`} />
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )
        })()}
      </div>
    </div>
  )
}
