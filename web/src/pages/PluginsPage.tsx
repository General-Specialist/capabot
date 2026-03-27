import { useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Trash2, Plus, X, Download, ChevronDown, Save, Loader2 } from 'lucide-react'
import { api, type Skill, type AgentTool } from '@/lib/api'
import { useAlert } from '@/components/AlertProvider'

const GO_PLACEHOLDER = `package main

import (
\t"encoding/json"
\t"fmt"
\t"os"
)

func main() {
\tvar params map[string]any
\tjson.NewDecoder(os.Stdin).Decode(&params)
\tfmt.Print(\`{"content":"hello","is_error":false}\`)
}`

export function PluginsPage() {
  const [tab, setTab] = useState<'custom' | 'clawhub'>('custom')
  const [plugins, setPlugins] = useState<Skill[]>([])
  const [tools, setTools] = useState<AgentTool[]>([])
  const [loading, setLoading] = useState(true)
  const [removing, setRemoving] = useState<Record<string, boolean>>({})

  // Create form
  const [showCreate, setShowCreate] = useState(false)
  const [createName, setCreateName] = useState('')
  const [createDesc, setCreateDesc] = useState('')
  const [createCode, setCreateCode] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const loadPlugins = () =>
    api.skills().then(res => setPlugins(res.filter(s => s.tier >= 2)))

  useEffect(() => {
    let cancelled = false
    Promise.all([api.skills(), api.tools()])
      .then(([sk, tl]) => {
        if (!cancelled) {
          setPlugins(sk.filter(s => s.tier >= 2))
          setTools(tl)
        }
      })
      .catch(() => {})
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  const uninstall = async (name: string) => {
    setRemoving(prev => ({ ...prev, [name]: true }))
    try {
      await fetch(`/api/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
      await loadPlugins()
    } catch {
      // silently fail
    } finally {
      setRemoving(prev => ({ ...prev, [name]: false }))
    }
  }

  const createPlugin = async () => {
    setCreateError(null)
    if (!createName.trim()) { setCreateError('Name is required'); return }
    if (!createCode.trim()) { setCreateError('Code is required'); return }
    setCreating(true)
    try {
      const res = await api.skillCreate({
        name: createName.trim(),
        description: createDesc.trim() || undefined,
        code: createCode,
      })
      if (res.success) {
        setCreateName('')
        setCreateDesc('')
        setCreateCode('')
        setShowCreate(false)
        await loadPlugins()
      }
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Creation failed')
    } finally {
      setCreating(false)
    }
  }

  // Install from GitHub
  const [installInput, setInstallInput] = useState('')
  const [installLoading, setInstallLoading] = useState(false)
  const [installResult, setInstallResult] = useState<{ success: boolean; message: string } | null>(null)

  const installFromGitHub = async () => {
    const value = installInput.trim()
    if (!value) return
    setInstallLoading(true)
    setInstallResult(null)
    try {
      const res = await api.skillsInstall(value)
      const msg = { success: res.success, message: res.success ? `Installed "${res.skill_name}"` : 'Install failed' }
      setInstallResult(msg)
      if (msg.success) setTimeout(() => setInstallResult(null), 5000)
      if (res.success) {
        setInstallInput('')
        await loadPlugins()
      }
    } catch (err) {
      setInstallResult({ success: false, message: err instanceof Error ? err.message : 'Install failed' })
    } finally {
      setInstallLoading(false)
    }
  }

  const custom = plugins.filter(p => p.tier === 2)
  const clawhub = plugins.filter(p => p.tier === 3)

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div className="flex gap-1 text-sm">
            {(['custom', 'clawhub'] as const).map(t => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={`px-3 py-1 rounded-md transition-colors ${tab === t ? 'bg-sidebar-white text-hover-black font-medium' : 'text-normal-black hover:text-hover-black'}`}
              >
                {t === 'custom' && <>Custom {custom.length > 0 && <span className="ml-1 text-xs text-normal-black">({custom.length})</span>}</>}
                {t === 'clawhub' && <>ClawHub {clawhub.length > 0 && <span className="ml-1 text-xs text-normal-black">({clawhub.length})</span>}</>}
              </button>
            ))}
          </div>
          {tab === 'custom' && (
            <button
              onClick={() => { setShowCreate(s => !s); setCreateError(null) }}
              className="flex items-center gap-1 text-xs text-normal-black hover:text-hover-black transition-colors"
            >
              {showCreate ? <X size={13} /> : <Plus size={13} />}
              {showCreate ? 'Cancel' : 'New'}
            </button>
          )}
        </div>

        <p className="text-xs text-normal-black mb-5">Executable scripts the agent can run as tools. Written in Go and compiled on save.</p>

        <AnimatePresence>
        {tab === 'custom' && showCreate && (
          <motion.div
            key="plugin-create"
            initial={{ opacity: 0, scale: 0.96, y: -4 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.96, y: -4 }}
            transition={{ duration: 0.18, ease: [0.4, 0, 0.2, 1] }}
            className="mb-6 space-y-3 p-4 rounded-2xl border border-border-white bg-sidebar-white"
          >
            <input
              value={createName}
              onChange={e => setCreateName(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ''))}
              placeholder="plugin-name"
              className="w-full px-4 py-2 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none font-mono"
            />
            <input
              value={createDesc}
              onChange={e => setCreateDesc(e.target.value)}
              placeholder="Description (optional)"
              className="w-full px-4 py-2 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none"
            />
            <textarea
              value={createCode}
              onChange={e => setCreateCode(e.target.value)}
              placeholder={GO_PLACEHOLDER}
              rows={14}
              className="w-full px-4 py-3 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none font-mono resize-y leading-relaxed"
              spellCheck={false}
            />
            {createError && <p className="text-sm text-red">{createError}</p>}
            <button
              onClick={() => void createPlugin()}
              disabled={creating}
              className="px-4 py-2 text-sm rounded-xl bg-brand-primary text-white hover:opacity-80 disabled:opacity-40 transition-opacity"
            >
              {creating ? 'Creating…' : 'Create'}
            </button>
          </motion.div>
        )}
        </AnimatePresence>

        {tab === 'custom' && !showCreate && (
          <CustomPluginList plugins={custom} tools={tools} loading={loading} removing={removing} onRemove={uninstall} />
        )}

        {tab === 'clawhub' && (
          <ClawHubTab
            installInput={installInput}
            setInstallInput={setInstallInput}
            installLoading={installLoading}
            installResult={installResult}
            onInstall={() => void installFromGitHub()}
            plugins={clawhub}
            loading={loading}
            removing={removing}
            onRemove={uninstall}
            tools={tools}
          />
        )}
      </div>
    </div>
  )
}

function ClawHubTab({ installInput, setInstallInput, installLoading, installResult, onInstall, plugins, loading, removing, onRemove, tools }: {
  installInput: string
  setInstallInput: (v: string) => void
  installLoading: boolean
  installResult: { success: boolean; message: string } | null
  onInstall: () => void
  plugins: Skill[]
  loading: boolean
  removing: Record<string, boolean>
  onRemove: (name: string) => void
  tools: AgentTool[]
}) {
  return (
    <>
      <div className="mb-6 space-y-2">
        <div className="flex items-center gap-2 text-sm font-mono">
          <span className="text-normal-black shrink-0">install</span>
          <input
            value={installInput}
            onChange={e => setInstallInput(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') onInstall() }}
            placeholder="name or owner/repo"
            className="flex-1 px-3 py-1.5 text-sm rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none font-mono"
          />
          <button
            onClick={onInstall}
            disabled={installLoading || !installInput.trim()}
            className="h-7 w-7 rounded-full flex items-center justify-center bg-brand-primary text-white hover:opacity-80 disabled:opacity-40 transition-opacity shrink-0"
          >
            {installLoading ? (
              <div className="w-3 h-3 border border-white border-t-transparent rounded-full animate-spin" />
            ) : (
              <Download size={12} />
            )}
          </button>
        </div>
        {installResult && (
          <p className={`text-xs font-mono ${installResult.success ? 'text-terminal-green' : 'text-red'}`}>
            {installResult.message}
          </p>
        )}
      </div>
      <ClawHubPluginList plugins={plugins} tools={tools} loading={loading} removing={removing} onRemove={onRemove} />
    </>
  )
}

function CustomPluginList({ plugins, tools, loading, removing, onRemove, empty = "No plugins yet. Plugins are tool calls — have an agent create one for you, or click New to upload a Go script." }: {
  plugins: Skill[]
  tools: AgentTool[]
  loading: boolean
  removing: Record<string, boolean>
  onRemove: (name: string) => void
  empty?: string
}) {
  const [expanded, setExpanded] = useState<string | null>(null)

  if (!loading && plugins.length === 0) {
    return <p className="text-sm text-normal-black">{empty}</p>
  }

  return (
    <div className="space-y-2">
      {plugins.map(plugin => {
        const pluginTools = tools.filter(t => t.plugin === plugin.name)
        const isOpen = expanded === plugin.name
        return (
          <div key={plugin.name} className="rounded-2xl border border-border-white overflow-hidden">
            <button
              className="w-full flex items-center gap-4 px-4 py-3 text-left"
              onClick={() => setExpanded(prev => prev === plugin.name ? null : plugin.name)}
            >
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <p className="text-sm font-medium text-hover-black truncate">{plugin.name}</p>
                  {plugin.version && (
                    <span className="text-xs text-normal-black font-mono shrink-0">{plugin.version}</span>
                  )}
                </div>
                {plugin.description && (
                  <p className="text-xs text-normal-black truncate mt-0.5">{plugin.description}</p>
                )}
              </div>
              <div className="flex items-center gap-2 shrink-0">
                {plugin.removable && (
                  <button
                    onClick={e => { e.stopPropagation(); onRemove(plugin.name) }}
                    disabled={removing[plugin.name]}
                    className="h-7 w-7 rounded-full flex items-center justify-center text-normal-black hover:text-red hover:bg-sidebar-white transition-colors disabled:opacity-40"
                  >
                    {removing[plugin.name]
                      ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" />
                      : <Trash2 size={13} />
                    }
                  </button>
                )}
                <ChevronDown size={13} className={`text-normal-black transition-transform ${isOpen ? '' : '-rotate-90'}`} />
              </div>
            </button>
            {isOpen && (
              <div className="border-t border-border-white px-4 py-3 bg-sidebar-white space-y-1">
                {pluginTools.length > 0 ? pluginTools.map(t => (
                  <div key={t.name} className="flex items-start gap-3 py-1">
                    <p className="text-xs font-medium text-hover-black font-mono shrink-0">{t.name}</p>
                    {t.description && <p className="text-xs text-normal-black">{t.description}</p>}
                  </div>
                )) : (
                  <p className="text-xs text-normal-black">No registered tools.</p>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function ClawHubPluginList({ plugins, tools, loading, removing, onRemove }: {
  plugins: Skill[]
  tools: AgentTool[]
  loading: boolean
  removing: Record<string, boolean>
  onRemove: (name: string) => void
}) {
  const [expanded, setExpanded] = useState<string | null>(null)

  if (!loading && plugins.length === 0) {
    return <p className="text-sm text-normal-black">No ClawHub plugins installed yet.</p>
  }

  return (
    <div className="space-y-2">
      {plugins.map(plugin => {
        const pluginTools = tools.filter(t => t.plugin === plugin.name)
        const hasSchema = plugin.config_schema && Object.keys(plugin.config_schema).length > 0
        const isOpen = expanded === plugin.name
        const isRemoving = removing[plugin.name]
        return (
          <div key={plugin.name} className="rounded-2xl border border-border-white overflow-hidden">
            <button
              className="w-full flex items-center gap-4 px-4 py-3 text-left"
              onClick={() => setExpanded(prev => prev === plugin.name ? null : plugin.name)}
            >
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <p className="text-sm font-medium text-hover-black truncate">{plugin.name}</p>
                  {plugin.version && (
                    <span className="text-xs text-normal-black font-mono shrink-0">{plugin.version}</span>
                  )}
                </div>
                {plugin.description && (
                  <p className="text-xs text-normal-black truncate mt-0.5">{plugin.description}</p>
                )}
              </div>
              <div className="flex items-center gap-2 shrink-0">
                {plugin.removable && (
                  <button
                    onClick={e => { e.stopPropagation(); onRemove(plugin.name) }}
                    disabled={isRemoving}
                    className="h-7 w-7 rounded-full flex items-center justify-center text-normal-black hover:text-red hover:bg-sidebar-white transition-colors disabled:opacity-40"
                  >
                    {isRemoving
                      ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" />
                      : <Trash2 size={13} />
                    }
                  </button>
                )}
                <ChevronDown size={13} className={`text-normal-black transition-transform ${isOpen ? '' : '-rotate-90'}`} />
              </div>
            </button>
            {isOpen && (
              <div className="border-t border-border-white bg-sidebar-white">
                <div className="px-4 py-3 space-y-1">
                  {pluginTools.length > 0 ? pluginTools.map(t => (
                    <div key={t.name} className="flex items-start gap-3 py-1">
                      <p className="text-xs font-medium text-hover-black font-mono shrink-0">{t.name}</p>
                      {t.description && <p className="text-xs text-normal-black">{t.description}</p>}
                    </div>
                  )) : (
                    <p className="text-xs text-normal-black">No registered tools.</p>
                  )}
                </div>
                {hasSchema && (
                  <PluginConfigForm pluginName={plugin.name} schema={plugin.config_schema!} />
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function PluginConfigForm({ pluginName, schema }: { pluginName: string; schema: Record<string, unknown> }) {
  const { alert } = useAlert()
  const [values, setValues] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const properties = (schema.properties ?? {}) as Record<string, { type?: string; description?: string; default?: unknown }>
  const required = new Set((schema.required ?? []) as string[])
  const fields = Object.entries(properties)

  useEffect(() => {
    api.skillConfigGet(pluginName)
      .then(cfg => {
        const v: Record<string, string> = {}
        for (const [key] of fields) {
          v[key] = cfg[key] != null ? String(cfg[key]) : ''
        }
        setValues(v)
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [pluginName]) // eslint-disable-line react-hooks/exhaustive-deps

  const save = async () => {
    setSaving(true)
    try {
      const config: Record<string, unknown> = {}
      for (const [key, def] of fields) {
        const val = values[key]
        if (val === '' && !required.has(key)) continue
        if (def.type === 'number' || def.type === 'integer') {
          config[key] = Number(val)
        } else if (def.type === 'boolean') {
          config[key] = val === 'true'
        } else {
          config[key] = val
        }
      }
      await api.skillConfigSet(pluginName, config)
      alert('Config saved. Restart to apply.', 'success')
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to save config', 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="border-t border-border-white px-4 py-4 bg-sidebar-white flex items-center gap-2 text-xs text-normal-black">
        <Loader2 size={12} className="animate-spin" /> Loading config…
      </div>
    )
  }

  if (fields.length === 0) {
    return (
      <div className="border-t border-border-white px-4 py-3 bg-sidebar-white text-xs text-normal-black">
        This plugin declares a config schema but has no configurable fields.
      </div>
    )
  }

  return (
    <div className="border-t border-border-white px-4 py-4 bg-sidebar-white space-y-3">
      {fields.map(([key, def]) => (
        <div key={key}>
          <label className="block text-xs font-medium text-hover-black mb-1">
            {key}{required.has(key) && <span className="text-red ml-0.5">*</span>}
          </label>
          {def.description && (
            <p className="text-[11px] text-normal-black mb-1">{def.description}</p>
          )}
          {def.type === 'boolean' ? (
            <select
              value={values[key] || 'false'}
              onChange={e => setValues(prev => ({ ...prev, [key]: e.target.value }))}
              className="w-full px-3 py-1.5 text-sm rounded-lg border border-border-white bg-white text-hover-black outline-none"
            >
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
          ) : (
            <input
              type={def.type === 'number' || def.type === 'integer' ? 'number' : 'text'}
              value={values[key] || ''}
              onChange={e => setValues(prev => ({ ...prev, [key]: e.target.value }))}
              placeholder={def.default != null ? String(def.default) : key}
              className="w-full px-3 py-1.5 text-sm rounded-lg border border-border-white bg-white text-hover-black outline-none font-mono"
            />
          )}
        </div>
      ))}
      <button
        onClick={() => void save()}
        disabled={saving}
        className="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded-lg bg-brand-primary text-white hover:opacity-80 disabled:opacity-40 transition-opacity"
      >
        {saving ? <Loader2 size={11} className="animate-spin" /> : <Save size={11} />}
        {saving ? 'Saving…' : 'Save config'}
      </button>
    </div>
  )
}
