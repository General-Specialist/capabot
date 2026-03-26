import { useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Plus, Play, Save, ChevronDown, ChevronRight, X } from 'lucide-react'
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
        className={`flex items-center gap-4 ${body ? 'cursor-pointer' : ''}`}
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

function AutomationFormBody({ form, setForm, error, saving, triggering, selected, onSave, onTrigger, onScheduleChange, skills }: {
  form: FormState
  setForm: React.Dispatch<React.SetStateAction<FormState>>
  error: string | null
  saving: boolean
  triggering: boolean
  selected: Automation | null
  onSave: () => void
  onTrigger: () => void
  onScheduleChange: (data: { start_at?: string | null; end_at?: string | null; start_offset?: string | null; end_offset?: string | null; rrule?: string | null }) => void
  skills: Skill[]
}) {
  return (
    <div className="flex flex-col gap-2">
      <DatePicker
        rrule={form.rrule || null}
        start_at={form.start_at}
        start_offset={form.start_offset}
        end_at={form.end_at}
        end_offset={form.end_offset}
        showRepeat={true}
        onChange={onScheduleChange}
      />
      <SkillPicker skills={skills} value={form.skill_names} onChange={names => setForm(f => ({ ...f, skill_names: names }))} />
      <textarea
        value={form.prompt}
        onChange={e => setForm(f => ({ ...f, prompt: e.target.value }))}
        placeholder={form.skill_names.length > 0 && form.skill_names.every(n => (skills.find(s => s.name === n)?.tier ?? 1) >= 2) ? 'Optional: what should the agent do?' : 'Prompt — what should the agent do?'}
        rows={4}
        className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none resize-none"
      />
      {form.skill_names.length === 1 && !form.prompt && (skills.find(s => s.name === form.skill_names[0])?.tier ?? 0) >= 2 && (
        <p className="text-xs text-brand-primary">Runs directly — no LLM tokens used</p>
      )}
      {error && <p className="text-xs text-red">{error}</p>}
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2">
          {selected && (
            <button onClick={onTrigger} disabled={triggering} className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-border-white rounded-capsule text-hover-black hover:bg-sidebar-white disabled:opacity-40 transition-colors">
              {triggering ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" /> : <Play size={12} />}
              Run now
            </button>
          )}
          <button onClick={onSave} disabled={saving} className="ml-auto flex items-center gap-1.5 px-4 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-capsule hover:opacity-80 disabled:opacity-40 transition-opacity">
            <Save size={13} />{saving ? 'Saving…' : 'Save'}
          </button>
        </div>
        {form.start_offset === 'P0D' && !selected && !form.start_at?.match(/T(?!00:00:00Z)/) && (
          <p className="text-xs text-normal-black">This will run immediately on creation</p>
        )}
      </div>
    </div>
  )
}

function NewAutomationForm({ form, setForm, error, saving, onSave, onClose, onScheduleChange, skills }: {
  form: FormState
  setForm: React.Dispatch<React.SetStateAction<FormState>>
  error: string | null
  saving: boolean
  onSave: () => void
  onClose: () => void
  onScheduleChange: (data: { start_at?: string | null; end_at?: string | null; start_offset?: string | null; end_offset?: string | null; rrule?: string | null }) => void
  skills: Skill[]
}) {
  return (
    <div className="rounded-2xl border border-border-white p-5 mt-1">
      <div className="flex items-start gap-2">
        <div className="flex-1 flex flex-col gap-2">
          <input
            value={form.name}
            onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
            placeholder="Name"
            className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
          />
          <AutomationFormBody form={form} setForm={setForm} error={error} saving={saving} triggering={false} selected={null} onSave={onSave} onTrigger={() => {}} onScheduleChange={onScheduleChange} skills={skills} />
        </div>
        <button type="button" onClick={onClose} className="p-1 rounded-lg text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors shrink-0 self-start">
          <X size={14} />
        </button>
      </div>
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
    setError(null)
    setForm({ name: a.name, prompt: a.prompt, skill_names: a.skill_names || [], enabled: a.enabled, rrule: a.rrule, start_at: a.start_at, start_offset: a.start_offset || null, end_offset: a.end_offset || null, end_at: a.end_at })
    loadRuns(a.id)
  }

  const startNew = () => {
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
        const noTimeSet = form.start_at?.endsWith('T00:00:00Z') ?? false
        if (form.start_offset === 'P0D' && noTimeSet) {
          api.automationTrigger(created.id).catch(() => {})
        }
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


  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        <div className="flex flex-col gap-4">
          <AnimatePresence mode="wait">
            {isNew ? (
              <motion.div
                key="form"
                initial={{ opacity: 0, scale: 0.96, y: -4 }}
                animate={{ opacity: 1, scale: 1, y: 0 }}
                exit={{ opacity: 0, scale: 0.96, y: -4 }}
                transition={{ duration: 0.18, ease: [0.4, 0, 0.2, 1] }}
              >
                <NewAutomationForm form={form} setForm={setForm} error={error} saving={saving} onSave={() => void save()} onClose={() => setIsNew(false)} onScheduleChange={handleScheduleChange} skills={skills} />
              </motion.div>
            ) : (
              <motion.div
                key="btn"
                initial={{ opacity: 0, scale: 0.96 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.96 }}
                transition={{ duration: 0.18, ease: [0.4, 0, 0.2, 1] }}
                className="flex"
              >
                <button
                  onClick={startNew}
                  className="flex items-center gap-1.5 px-3 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-capsule hover:opacity-80 transition-opacity"
                >
                  <Plus size={13} />
                  New
                </button>
              </motion.div>
            )}
          </AnimatePresence>

          {/* List */}
          {!loading && automations.length === 0 && !isNew ? (
            <p className="text-sm text-normal-black">No automations yet. Click <strong>New</strong> to create one.</p>
          ) : (
            <div className="space-y-0">
              {automations.map(a => {
                const isExpanded = selected?.id === a.id
                return (
                  <div key={a.id}>
                    {isExpanded ? (
                      <div className="rounded-2xl border border-border-white p-4 mb-2">
                        <div className="flex items-center gap-2 mb-1">
                          <input
                            value={form.name}
                            onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                            className="flex-1 text-sm font-medium text-hover-black bg-transparent outline-none min-w-0 placeholder:text-normal-black"
                            placeholder="Name"
                          />
                          <button type="button" onClick={() => setSelected(null)} className="p-1 rounded-lg text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors shrink-0">
                            <ChevronDown size={14} />
                          </button>
                        </div>
                        <p className="text-xs text-normal-black mb-3">
                          Next: {formatFuture(a.next_run_at)}
                        </p>
                        <AutomationFormBody form={form} setForm={setForm} error={error} saving={saving} triggering={triggering} selected={selected} onSave={() => void save()} onTrigger={() => void trigger()} onScheduleChange={handleScheduleChange} skills={skills} />
                        {runs.length > 0 && (
                          <div className="mt-3">
                            <div className="space-y-1">
                              {runs.map(run => <RunRow key={run.id} run={run} />)}
                            </div>
                          </div>
                        )}
                      </div>
                    ) : (
                      <button
                        onClick={() => selectAutomation(a)}
                        className="w-full text-left flex items-center gap-4 px-3 py-2.5 rounded-xl hover:bg-sidebar-white transition-colors"
                      >
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-medium text-hover-black truncate">{a.name}</span>
                            {!a.enabled && (
                              <span className="text-[10px] text-normal-black bg-icon-hover-white px-1.5 py-0.5 rounded shrink-0">off</span>
                            )}
                          </div>
                          <p className="text-xs text-normal-black mt-0.5">
                            Next: {formatFuture(a.next_run_at)}
                          </p>
                        </div>
                        <ChevronDown size={13} className="text-normal-black shrink-0 -rotate-90" />
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
