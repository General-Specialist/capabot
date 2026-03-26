import { useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Trash2, Plus, X, Download, ChevronDown } from 'lucide-react'
import { api, type Skill } from '@/lib/api'

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
  const [tab, setTab] = useState<'custom' | 'openclaw'>('custom') // openclaw tab for OpenClaw plugins
  const [plugins, setPlugins] = useState<Skill[]>([])
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
    api.skills()
      .then(res => { if (!cancelled) setPlugins(res.filter(s => s.tier >= 2)) })
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
  const openclaw = plugins.filter(p => p.tier === 3)

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div className="flex gap-1 text-sm">
            {(['custom', 'openclaw'] as const).map(t => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={`px-3 py-1 rounded-md transition-colors ${tab === t ? 'bg-sidebar-white text-hover-black font-medium' : 'text-normal-black hover:text-hover-black'}`}
              >
                {t === 'custom' && <>Custom {custom.length > 0 && <span className="ml-1 text-xs text-normal-black">({custom.length})</span>}</>}
                {t === 'openclaw' && <>OpenClaw {openclaw.length > 0 && <span className="ml-1 text-xs text-normal-black">({openclaw.length})</span>}</>}
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
          <PluginList plugins={custom} loading={loading} removing={removing} onRemove={uninstall} empty="No plugins yet. Plugins are tool calls — have an agent create one for you, or click New to upload a Go script." />
        )}

        {tab === 'openclaw' && (
          <>
            <div className="mb-6 space-y-2">
              <div className="flex items-center gap-2 text-sm font-mono">
                <span className="text-normal-black shrink-0">openclaw plugins install</span>
                <input
                  value={installInput}
                  onChange={e => setInstallInput(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') void installFromGitHub() }}
                  placeholder="name or owner/repo"
                  className="flex-1 px-3 py-1.5 text-sm rounded-lg border border-border-white bg-sidebar-white text-hover-black outline-none font-mono"
                />
                <button
                  onClick={() => void installFromGitHub()}
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
            <PluginList plugins={openclaw} loading={loading} removing={removing} onRemove={uninstall} empty="No OpenClaw plugins installed yet." />
          </>
        )}
      </div>
    </div>
  )
}

function PluginList({ plugins, loading, removing, onRemove, empty }: {
  plugins: Skill[]
  loading: boolean
  removing: Record<string, boolean>
  onRemove: (name: string) => void
  empty: string
}) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const [code, setCode] = useState<Record<string, string>>({})

  const toggle = (name: string) => {
    if (expanded === name) { setExpanded(null); return }
    setExpanded(name)
    if (!(name in code)) {
      api.skillGet(name).then(res => setCode(prev => ({ ...prev, [name]: res.code }))).catch(() => {})
    }
  }

  if (!loading && plugins.length === 0) {
    return <p className="text-sm text-normal-black">{empty}</p>
  }

  return (
    <div className="space-y-2">
      {plugins.map(plugin => {
        const isOpen = expanded === plugin.name
        return (
          <div key={plugin.name} className="rounded-2xl border border-border-white">
            <button
              className="w-full flex items-center gap-4 px-4 py-3 text-left"
              onClick={() => toggle(plugin.name)}
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
              <ChevronDown size={13} className={`text-normal-black shrink-0 transition-transform ${isOpen ? '' : '-rotate-90'}`} />
            </button>
            {isOpen && (
              <div className="px-4 pb-4 flex flex-col gap-3">
                {plugin.instructions && (
                  <p className="text-xs text-normal-black whitespace-pre-wrap">{plugin.instructions}</p>
                )}
                {code[plugin.name] !== undefined
                  ? <pre className="text-xs font-mono text-hover-black bg-sidebar-white rounded-xl p-3 overflow-x-auto whitespace-pre">{code[plugin.name]}</pre>
                  : <div className="w-4 h-4 border border-border-white border-t-transparent rounded-full animate-spin" />
                }
                {plugin.removable && (
                  <button
                    onClick={() => onRemove(plugin.name)}
                    disabled={removing[plugin.name]}
                    className="flex items-center gap-1.5 text-xs text-normal-black hover:text-red disabled:opacity-40 transition-colors w-fit"
                  >
                    {removing[plugin.name]
                      ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" />
                      : <Trash2 size={13} />
                    }
                    Uninstall
                  </button>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
