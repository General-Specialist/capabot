import { useEffect, useState } from 'react'
import { Plus, Play, Trash2, ChevronRight } from 'lucide-react'
import { api, type Automation, type AutomationRun, type Skill } from '@/lib/api'
import DatePicker from '@/components/DatePicker'

function formatRelative(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso.includes('T') ? iso : iso + 'Z')
  const diff = Date.now() - d.getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return `${Math.floor(diff / 86_400_000)}d ago`
}

function formatFuture(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso.includes('T') ? iso : iso + 'Z')
  const diff = d.getTime() - Date.now()
  if (diff < 0) return 'due now'
  if (diff < 60_000) return 'in <1m'
  if (diff < 3_600_000) return `in ${Math.floor(diff / 60_000)}m`
  if (diff < 86_400_000) return `in ${Math.floor(diff / 3_600_000)}h`
  return `in ${Math.floor(diff / 86_400_000)}d`
}

const EMPTY_FORM = { name: '', prompt: '', skill_name: '', enabled: true, rrule: '', start_at: null as string | null, end_at: null as string | null }

export function AutomationsPage() {
  const [automations, setAutomations] = useState<Automation[]>([])
  const [selected, setSelected] = useState<Automation | null>(null)
  const [runs, setRuns] = useState<AutomationRun[]>([])
  const [form, setForm] = useState(EMPTY_FORM)
  const [skills, setSkills] = useState<Skill[]>([])
  const [isNew, setIsNew] = useState(false)
  const [saving, setSaving] = useState(false)
  const [triggering, setTriggering] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const load = () => api.automations().then(setAutomations).catch(() => {})

  useEffect(() => {
    load().finally(() => setLoading(false))
    api.skills().then(setSkills).catch(() => {})
  }, [])

  const loadRuns = (id: number) =>
    api.automationRuns(id).then(setRuns).catch(() => setRuns([]))

  const selectAutomation = (a: Automation) => {
    setSelected(a)
    setIsNew(false)
    setError(null)
    setForm({ name: a.name, prompt: a.prompt, skill_name: a.skill_name || '', enabled: a.enabled, rrule: a.rrule, start_at: a.start_at, end_at: a.end_at })
    loadRuns(a.id)
  }

  const startNew = () => {
    setSelected(null)
    setIsNew(true)
    setError(null)
    setForm(EMPTY_FORM)
    setRuns([])
  }

  const handleScheduleChange = (data: {
    absoluteStartUtc?: string | null
    absoluteEndUtc?: string | null
    startOffsetRule?: string | null
    endOffsetRule?: string | null
    recurrenceRule?: string | null
  }) => {
    setForm(f => ({
      ...f,
      rrule: data.recurrenceRule || '',
      start_at: data.absoluteStartUtc || null,
      end_at: data.absoluteEndUtc || null,
    }))
  }

  const save = async () => {
    setError(null)
    setSaving(true)
    try {
      if (isNew) {
        const created = await api.automationCreate({
          name: form.name,
          prompt: form.prompt,
          skill_name: form.skill_name,
          rrule: form.rrule,
          start_at: form.start_at,
          end_at: form.end_at,
          enabled: form.enabled,
        })
        setAutomations(prev => [...prev, created])
        selectAutomation(created)
      } else if (selected) {
        const updated = await api.automationUpdate(selected.id, {
          name: form.name,
          prompt: form.prompt,
          skill_name: form.skill_name,
          rrule: form.rrule,
          start_at: form.start_at,
          end_at: form.end_at,
          enabled: form.enabled,
        })
        setAutomations(prev => prev.map(a => a.id === updated.id ? updated : a))
        setSelected(updated)
        setForm({ name: updated.name, prompt: updated.prompt, skill_name: updated.skill_name || '', enabled: updated.enabled, rrule: updated.rrule, start_at: updated.start_at, end_at: updated.end_at })
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  const trigger = async () => {
    if (!selected) return
    setTriggering(true)
    try {
      await api.automationTrigger(selected.id)
      setTimeout(() => loadRuns(selected.id), 1500)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Trigger failed')
    } finally {
      setTriggering(false)
    }
  }

  const remove = async () => {
    if (!selected) return
    setDeleting(true)
    try {
      await api.automationDelete(selected.id)
      setAutomations(prev => prev.filter(a => a.id !== selected.id))
      setSelected(null)
      setIsNew(false)
      setRuns([])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed')
    } finally {
      setDeleting(false)
    }
  }

  const showPanel = isNew || selected !== null

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-4xl">
        <div className="flex items-center justify-end mb-6">
          <button
            onClick={startNew}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-lg hover:opacity-80 transition-opacity"
          >
            <Plus size={13} />
            New
          </button>
        </div>

        <div className="flex gap-4">
          {/* List */}
          <div className={`${showPanel ? 'w-64 shrink-0' : 'w-full'}`}>
            {loading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <div key={i} className="h-14 rounded-lg animate-pulse bg-icon-hover-white" />
                ))}
              </div>
            ) : automations.length === 0 && !showPanel ? (
              <p className="text-sm text-normal-black">No automations yet. Click <strong>New</strong> to create one.</p>
            ) : (
              <div className="space-y-1">
                {automations.map(a => (
                  <button
                    key={a.id}
                    onClick={() => selectAutomation(a)}
                    className={`w-full text-left flex items-center gap-3 px-3 py-2.5 rounded-lg transition-colors ${
                      selected?.id === a.id ? 'bg-icon-hover-white' : 'hover:bg-sidebar-white'
                    }`}
                  >
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-hover-black truncate">{a.name}</span>
                        {a.skill_name && (
                          <span className="text-[10px] text-brand-primary bg-brand-primary/10 px-1.5 py-0.5 rounded shrink-0">skill</span>
                        )}
                        {!a.enabled && (
                          <span className="text-[10px] text-normal-black bg-icon-hover-white px-1.5 py-0.5 rounded shrink-0">off</span>
                        )}
                      </div>
                      <p className="text-xs text-normal-black font-mono mt-0.5 truncate">{a.skill_name || a.rrule || '—'}</p>
                    </div>
                    <ChevronRight size={13} className="text-normal-black shrink-0" />
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* Detail / form panel */}
          {showPanel && (
            <div className="flex-1 min-w-0">
              <div className="rounded-lg border border-border-white p-5 space-y-4">
                <div className="space-y-3">
                  <input
                    value={form.name}
                    onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                    placeholder="Name"
                    className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none"
                  />
                  <div className="px-3 rounded-lg border border-border-white bg-sidebar-white">
                    <DatePicker
                      recurrenceRule={form.rrule || null}
                      absoluteStartUtc={form.start_at}
                      absoluteEndUtc={form.end_at}
                      showRepeat={true}
                      onChange={handleScheduleChange}
                    />
                  </div>

                  {/* Action: skill or prompt */}
                  <div>
                    <label className="text-xs text-normal-black mb-1 block">Action</label>
                    <select
                      value={form.skill_name}
                      onChange={e => setForm(f => ({ ...f, skill_name: e.target.value }))}
                      className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none"
                    >
                      <option value="">Agent (LLM prompt)</option>
                      {skills.map(s => (
                        <option key={s.name} value={s.name}>{s.name}</option>
                      ))}
                    </select>
                  </div>

                  {!form.skill_name && (
                    <textarea
                      value={form.prompt}
                      onChange={e => setForm(f => ({ ...f, prompt: e.target.value }))}
                      placeholder="Prompt — what should the agent do?"
                      rows={4}
                      className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none resize-none"
                    />
                  )}

                  {form.skill_name && (
                    <textarea
                      value={form.prompt}
                      onChange={e => setForm(f => ({ ...f, prompt: e.target.value }))}
                      placeholder="Input data for the skill (optional JSON)"
                      rows={2}
                      className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none resize-none"
                    />
                  )}
                </div>

                {error && <p className="text-xs text-red">{error}</p>}

                <div className="flex items-center gap-2">
                  <button
                    onClick={() => void save()}
                    disabled={saving}
                    className="px-4 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-lg hover:opacity-80 disabled:opacity-40 transition-opacity"
                  >
                    {saving ? 'Saving…' : 'Save'}
                  </button>
                  {selected && (
                    <>
                      <button
                        onClick={() => void trigger()}
                        disabled={triggering}
                        className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-border-white rounded-lg text-hover-black hover:bg-sidebar-white disabled:opacity-40 transition-colors"
                      >
                        {triggering
                          ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" />
                          : <Play size={12} />
                        }
                        Run now
                      </button>
                      <button
                        onClick={() => void remove()}
                        disabled={deleting}
                        className="ml-auto flex items-center gap-1.5 px-3 py-1.5 text-sm text-normal-black hover:text-red hover:bg-sidebar-white border border-border-white rounded-lg disabled:opacity-40 transition-colors"
                      >
                        <Trash2 size={12} />
                        Delete
                      </button>
                    </>
                  )}
                </div>

                {selected && (
                  <div className="text-xs text-normal-black flex gap-4 pt-1">
                    <span>Last run: {formatRelative(selected.last_run_at)}</span>
                    <span>Next: {formatFuture(selected.next_run_at)}</span>
                  </div>
                )}
              </div>

              {/* Run history */}
              {selected && (
                <div className="mt-4">
                  <p className="text-xs font-medium text-normal-black mb-2">Recent runs</p>
                  {runs.length === 0 ? (
                    <p className="text-xs text-normal-black">No runs yet.</p>
                  ) : (
                    <div className="space-y-1">
                      {runs.map(run => (
                        <div key={run.id} className="flex items-start gap-3 px-3 py-2 rounded-lg bg-sidebar-white text-xs">
                          <span className={`shrink-0 mt-0.5 w-2 h-2 rounded-full ${
                            run.status === 'success' ? 'bg-green-500' :
                            run.status === 'error' ? 'bg-red' : 'bg-normal-black animate-pulse'
                          }`} />
                          <div className="flex-1 min-w-0">
                            <span className="text-normal-black">{formatRelative(run.started_at)}</span>
                            {run.response && <p className="text-hover-black mt-0.5 truncate">{run.response}</p>}
                            {run.error && <p className="text-red mt-0.5 truncate">{run.error}</p>}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
