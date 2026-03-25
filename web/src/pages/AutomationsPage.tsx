import { useEffect, useState } from 'react'
import { Plus, Play, Trash2, ChevronDown, ChevronRight, X } from 'lucide-react'
import { Markdown } from '@/components/Markdown'
import { api, type Automation, type AutomationRun, type Skill } from '@/lib/api'
import DatePicker from '@/components/DatePicker'
import SkillPicker from '@/components/SkillPicker'

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

function RunRow({ run }: { run: AutomationRun }) {
  const [expanded, setExpanded] = useState(false)
  const body = run.error || run.response
  const isError = run.status === 'error'

  return (
    <div className="text-xs py-1.5">
      <div
        className={`flex items-center gap-3 ${body ? 'cursor-pointer' : ''}`}
        onClick={() => { if (body) setExpanded(e => !e) }}
      >
        <span className={`shrink-0 w-1.5 h-1.5 rounded-full ${
          run.status === 'success' ? 'bg-terminal-green' :
          run.status === 'error' ? 'bg-terminal-red' : 'bg-normal-black animate-pulse'
        }`} />
        <span className={`shrink-0 font-medium ${isError ? 'text-terminal-red' : run.status === 'success' ? 'text-terminal-green' : 'text-normal-black'}`}>
          {run.status}
        </span>
        <span className="text-normal-black">{formatRelative(run.started_at)}</span>
        {body && <ChevronRight size={12} className={`ml-auto text-normal-black shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />}
      </div>
      {expanded && body && (
        <div className={`pl-4 pt-1 pb-2 ${isError ? 'text-terminal-red' : 'text-sm leading-relaxed text-hover-black prose prose-sm max-w-none [&_*]:text-inherit [&_p]:my-1 [&_pre]:bg-icon-white [&_pre]:rounded-lg [&_pre]:p-3 [&_code]:text-xs [&_p:last-child]:mb-0'}`}>
          {isError
            ? <p className="font-mono text-xs whitespace-pre-wrap">{body}</p>
            : <Markdown>{body}</Markdown>
          }
        </div>
      )}
    </div>
  )
}

function todayUTCString(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}T00:00:00Z`
}

const EMPTY_FORM = { name: '', prompt: '', skill_names: [] as string[], enabled: true, rrule: 'FREQ=DAILY', start_at: todayUTCString() as string | null, start_offset: 'P0D' as string | null, end_offset: null as string | null, end_at: null as string | null }

type FormState = typeof EMPTY_FORM

function AutomationForm({ form, setForm, error, saving, triggering, deleting, selected, onSave, onTrigger, onDelete, onClose, onScheduleChange, skills }: {
  form: FormState
  setForm: React.Dispatch<React.SetStateAction<FormState>>
  error: string | null
  saving: boolean
  triggering: boolean
  deleting: boolean
  selected: Automation | null
  onSave: () => void
  onTrigger: () => void
  onDelete: () => void
  onClose?: () => void
  onScheduleChange: (data: { start_at?: string | null; end_at?: string | null; start_offset?: string | null; end_offset?: string | null; rrule?: string | null }) => void
  skills: Skill[]
}) {
  return (
    <div className="rounded-2xl border border-border-white p-5 mt-1">
      <div className="flex gap-3">
        <div className="flex-1 space-y-3">
        <div className="flex items-center gap-2">
          <input
            value={form.name}
            onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
            placeholder="Name"
            className="flex-1 text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
          />
          <div>
          <DatePicker
            rrule={form.rrule || null}
            start_at={form.start_at}
            start_offset={form.start_offset}
            end_at={form.end_at}
            end_offset={form.end_offset}
            showRepeat={true}
            onChange={onScheduleChange}
          />
          </div>
        </div>
        <SkillPicker skills={skills} value={form.skill_names} onChange={names => setForm(f => ({ ...f, skill_names: names }))} />
        <textarea
          value={form.prompt}
          onChange={e => setForm(f => ({ ...f, prompt: e.target.value }))}
          placeholder={form.skill_names.length > 0 ? 'Optional: add a prompt to let the agent decide when/how to use these skills' : 'Prompt — what should the agent do?'}
          rows={form.skill_names.length > 0 ? 2 : 4}
          className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none resize-none"
        />
        {form.skill_names.length === 1 && !form.prompt && (skills.find(s => s.name === form.skill_names[0])?.tier ?? 0) >= 2 && (
          <p className="text-xs text-brand-primary">Runs directly — no LLM tokens used</p>
        )}

        {error && <p className="text-xs text-red">{error}</p>}

        <div className="flex items-center gap-2">
          <button onClick={onSave} disabled={saving} className="px-4 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-capsule hover:opacity-80 disabled:opacity-40 transition-opacity">
            {saving ? 'Saving…' : 'Save'}
          </button>
          {selected && (
            <>
              <button onClick={onTrigger} disabled={triggering} className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-border-white rounded-capsule text-hover-black hover:bg-sidebar-white disabled:opacity-40 transition-colors">
                {triggering ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" /> : <Play size={12} />}
                Run now
              </button>
              <button onClick={onDelete} disabled={deleting} className="ml-auto p-1.5 text-normal-black hover:text-red hover:bg-sidebar-white rounded-lg disabled:opacity-40 transition-colors">
                <Trash2 size={14} />
              </button>
            </>
          )}
        </div>
        </div>
        {onClose && (
          <button type="button" onClick={onClose} className="p-1 rounded-lg text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors shrink-0 self-start">
            <X size={14} />
          </button>
        )}
      </div>

      {selected && (
        <>
          <div className="text-xs text-normal-black flex gap-4 pt-1">
            <span>Last run: {formatRelative(selected.last_run_at)}</span>
            <span>Next: {formatFuture(selected.next_run_at)}</span>
          </div>
        </>
      )}
    </div>
  )
}

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

  const load = () => api.automations().then(setAutomations).catch(() => {})

  useEffect(() => {
    load()
    api.skills().then(setSkills).catch(() => {})
  }, [])

  const loadRuns = (id: number) =>
    api.automationRuns(id).then(setRuns).catch(() => setRuns([]))

  const selectAutomation = (a: Automation) => {
    setSelected(a)
    setIsNew(false)
    setError(null)
    setForm({ name: a.name, prompt: a.prompt, skill_names: a.skill_names || [], enabled: a.enabled, rrule: a.rrule, start_at: a.start_at, start_offset: a.start_offset || null, end_offset: a.end_offset || null, end_at: a.end_at })
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
    start_at?: string | null
    end_at?: string | null
    start_offset?: string | null
    end_offset?: string | null
    rrule?: string | null
  }) => {
    setForm(f => ({
      ...f,
      rrule: data.rrule || '',
      start_at: data.start_offset === 'P0D' ? todayUTCString() : (data.start_at || null),
      start_offset: data.start_offset || null,
      end_at: data.end_at || null,
      end_offset: data.end_offset || null,
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
          skill_names: form.skill_names,
          start_offset: form.start_offset || '',
          end_offset: form.end_offset || '',
          rrule: form.rrule,
          start_at: form.start_at,
          end_at: form.end_at,
          enabled: form.enabled,
        })
        setAutomations(prev => [...prev, created])
        setIsNew(false)
        setSelected(null)
      } else if (selected) {
        const updated = await api.automationUpdate(selected.id, {
          name: form.name,
          prompt: form.prompt,
          skill_names: form.skill_names,
          start_offset: form.start_offset || '',
          end_offset: form.end_offset || '',
          rrule: form.rrule,
          start_at: form.start_at,
          end_at: form.end_at,
          enabled: form.enabled,
        })
        setAutomations(prev => prev.map(a => a.id === updated.id ? updated : a))
        setSelected(updated)
        setForm({ name: updated.name, prompt: updated.prompt, skill_names: updated.skill_names || [], enabled: updated.enabled, rrule: updated.rrule, start_at: updated.start_at, start_offset: updated.start_offset || null, end_offset: updated.end_offset || null, end_at: updated.end_at })
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


  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        <div className="flex items-center mb-6">
          <button
            onClick={startNew}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-capsule hover:opacity-80 transition-opacity"
          >
            <Plus size={13} />
            New
          </button>
        </div>

        <div className="flex flex-col gap-1">
          {/* New automation form — appears above the list */}
          {isNew && <AutomationForm form={form} setForm={setForm} error={error} saving={saving} triggering={false} deleting={false} selected={null} onSave={() => void save()} onTrigger={() => {}} onDelete={() => {}} onClose={() => setIsNew(false)} onScheduleChange={handleScheduleChange} skills={skills} />}

          {/* List */}
          {automations.length === 0 && !isNew ? (
            <p className="text-sm text-normal-black">No automations yet. Click <strong>New</strong> to create one.</p>
          ) : (
            <div className="space-y-0">
              {automations.map(a => (
                <div key={a.id}>
                  <button
                    onClick={() => selected?.id === a.id ? setSelected(null) : selectAutomation(a)}
                    className={`w-full text-left flex items-center gap-3 px-3 py-2.5 rounded-xl transition-colors ${
                      selected?.id === a.id ? 'bg-icon-hover-white' : 'hover:bg-sidebar-white'
                    }`}
                  >
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-hover-black truncate">{a.name}</span>
                        {a.skill_names?.length > 0 && (
                          <span className="text-[10px] text-brand-primary bg-brand-primary/10 px-1.5 py-0.5 rounded shrink-0">skill</span>
                        )}
                        {!a.enabled && (
                          <span className="text-[10px] text-normal-black bg-icon-hover-white px-1.5 py-0.5 rounded shrink-0">off</span>
                        )}
                      </div>
                      <p className="text-xs text-normal-black font-mono mt-0.5 truncate">{a.skill_names?.join(', ') || a.rrule || '—'}</p>
                    </div>
                    <ChevronDown size={13} className={`text-normal-black shrink-0 transition-transform ${selected?.id === a.id ? '' : '-rotate-90'}`} />
                  </button>
                  {selected?.id === a.id && (
                    <div className="mx-1 mb-2 space-y-3">
                      <AutomationForm form={form} setForm={setForm} error={error} saving={saving} triggering={triggering} deleting={deleting} selected={selected} onSave={() => void save()} onTrigger={() => void trigger()} onDelete={() => void remove()} onScheduleChange={handleScheduleChange} skills={skills} />
                      {runs.length > 0 && (
                        <div>
                          <p className="text-xs font-medium text-normal-black mb-2 px-1">Recent runs</p>
                          <div className="space-y-1">
                            {runs.map(run => <RunRow key={run.id} run={run} />)}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
