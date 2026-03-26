import { useEffect, useRef, useState } from 'react'
import { api, type ProviderKeys, type ProviderInfo, type TransportKeys } from '@/lib/api'

function useDarkMode() {
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem('darkMode')
    return stored !== null ? stored === 'true' : window.matchMedia('(prefers-color-scheme: dark)').matches
  })

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('darkMode', String(dark))
  }, [dark])

  const toggle = () => setDark(d => !d)
  return { dark, toggle }
}

const PROVIDERS: { key: 'anthropic' | 'openai' | 'gemini' | 'openrouter'; label: string; placeholder: string; href: string; hintLabel?: string }[] = [
  { key: 'anthropic',  label: 'Anthropic',  placeholder: 'sk-ant-...', href: 'https://console.anthropic.com/settings/keys' },
  { key: 'openai',     label: 'OpenAI',     placeholder: 'sk-...',     href: 'https://platform.openai.com/api-keys' },
  { key: 'gemini',     label: 'Gemini',     placeholder: 'AIza...',    href: 'https://aistudio.google.com/app/apikey', hintLabel: 'Get free API key' },
  { key: 'openrouter', label: 'OpenRouter', placeholder: 'sk-or-...',  href: 'https://openrouter.ai/settings/keys' },
]

const EMPTY: ProviderKeys = { anthropic: '', openai: '', gemini: '', openrouter: '' }
const EMPTY_TRANSPORT: TransportKeys = { discord_token: '', discord_app_id: '', discord_guild_id: '', slack_app_token: '', slack_bot_token: '', telegram_token: '' }

const TRANSPORT_GROUPS: { label: string; href?: string; fields: { key: keyof TransportKeys; label: string; placeholder: string; hint: string; secret?: boolean }[] }[] = [
  {
    label: 'Discord',
    href: 'https://discord.com/developers/applications',
    fields: [
      { key: 'discord_token',    label: 'Bot token',       placeholder: 'Bot token',            secret: true, hint: 'Bot → Token' },
      { key: 'discord_app_id',   label: 'Application ID',  placeholder: 'Application ID',                     hint: 'General Information → Application ID' },
      { key: 'discord_guild_id', label: 'Guild ID',        placeholder: 'Server ID (optional)',                hint: 'Settings → Advanced → Developer Mode, then right-click server → Copy Server ID' },
    ],
  },
  {
    label: 'Slack',
    href: 'https://api.slack.com/apps',
    fields: [
      { key: 'slack_app_token',  label: 'App token',       placeholder: 'xapp-...', secret: true, hint: 'Basic Information → App-Level Tokens' },
      { key: 'slack_bot_token',  label: 'Bot token',       placeholder: 'xoxb-...', secret: true, hint: 'Install App → Bot User OAuth Token' },
    ],
  },
  {
    label: 'Telegram',
    fields: [
      { key: 'telegram_token',   label: 'Bot token',       placeholder: 'Bot token', secret: true, hint: 'Message @BotFather → /newbot' },
    ],
  },
]

type Section = 'general' | 'api-keys' | 'transports'

const NAV: { id: Section; label: string }[] = [
  { id: 'general',    label: 'General' },
  { id: 'api-keys',   label: 'API keys' },
  { id: 'transports', label: 'Transports' },
]

function Toggle({ on, onClick }: { on: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={`relative w-10 h-5 rounded-full transition-colors flex-shrink-0 ${on ? 'bg-brand-primary' : 'bg-sidebar-hover-white'}`}
    >
      <span className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${on ? 'translate-x-5' : 'translate-x-0'}`} />
    </button>
  )
}

export function SettingsPage() {
  const [section, setSection] = useState<Section>('general')
  const [keys, setKeys] = useState<ProviderKeys>(EMPTY)
  const [transportKeys, setTransportKeys] = useState<TransportKeys>(EMPTY_TRANSPORT)
  const transportSaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const transportInitial = useRef(true)
  const [executeFallback, setExecuteFallback] = useState(false)
  const [shellMode, setShellMode] = useState('allowlist')
  const [shellApproved, setShellApproved] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [modes, setModes] = useState<Record<string, ProviderKeys>>({})
  const [activeMode, setActiveMode] = useState('default')
  const [selectedMode, setSelectedMode] = useState('default')
  const { dark, toggle } = useDarkMode()
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const initialLoad = useRef(true)
  const modeTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({})
  const modesInitial = useRef(true)

  useEffect(() => {
    api.configKeys().then(setKeys).catch(() => {})
    api.transportKeys().then(setTransportKeys).catch(() => {})
    api.executeFallbackGet().then(r => setExecuteFallback(r.enabled)).catch(() => {})
    api.shellModeGet().then(r => setShellMode(r.shell_mode)).catch(() => {})
    api.shellApprovedGet().then(r => setShellApproved(r.commands)).catch(() => {})
    api.providers().then(setProviders).catch(() => {})
    api.modes().then(r => {
      setModes(r.modes)
      setActiveMode(r.active)
    }).catch(() => {})
  }, [])

  useEffect(() => {
    if (initialLoad.current) { initialLoad.current = false; return }
    if (saveTimer.current) clearTimeout(saveTimer.current)
    saveTimer.current = setTimeout(() => {
      api.configKeysSave(keys).catch(err => setError(err instanceof Error ? err.message : 'Save failed'))
    }, 600)
    return () => { if (saveTimer.current) clearTimeout(saveTimer.current) }
  }, [keys])

  useEffect(() => {
    if (transportInitial.current) { transportInitial.current = false; return }
    if (transportSaveTimer.current) clearTimeout(transportSaveTimer.current)
    transportSaveTimer.current = setTimeout(() => {
      api.transportKeysSave(transportKeys).catch(err => setError(err instanceof Error ? err.message : 'Save failed'))
    }, 600)
    return () => { if (transportSaveTimer.current) clearTimeout(transportSaveTimer.current) }
  }, [transportKeys])

  const saveModeKeys = (name: string, modeKeys: ProviderKeys) => {
    if (modesInitial.current) return
    if (modeTimers.current[name]) clearTimeout(modeTimers.current[name])
    modeTimers.current[name] = setTimeout(() => {
      api.modeSet(name, modeKeys).catch(() => {})
    }, 600)
  }

  const updateModeKey = (modeName: string, key: 'anthropic' | 'openai' | 'gemini' | 'openrouter', value: string) => {
    setModes(prev => {
      const updated = { ...prev, [modeName]: { ...(prev[modeName] || EMPTY), [key]: value } }
      saveModeKeys(modeName, updated[modeName])
      return updated
    })
  }

  const updateModeModel = (modeName: string, model: string) => {
    setModes(prev => {
      const updated = { ...prev, [modeName]: { ...(prev[modeName] || EMPTY), model } }
      saveModeKeys(modeName, updated[modeName])
      return updated
    })
  }

  const updateModeSummarizationModel = (modeName: string, summarization_model: string) => {
    setModes(prev => {
      const updated = { ...prev, [modeName]: { ...(prev[modeName] || EMPTY), summarization_model } }
      saveModeKeys(modeName, updated[modeName])
      return updated
    })
  }

  const clearModeKeys = (modeName: string) => {
    const empty = { ...EMPTY }
    setModes(prev => ({ ...prev, [modeName]: empty }))
    api.modeSet(modeName, empty).catch(() => {})
  }

  const switchMode = (mode: string) => {
    setActiveMode(mode)
    api.activeModeSet(mode).catch(() => {})
  }

  useEffect(() => {
    if (Object.keys(modes).length > 0) modesInitial.current = false
  }, [modes])

  const allModels = providers.flatMap(p => p.models.map(m => ({ ...m, provider: p.name })))
  const builtIn = ['default', 'chat', 'execute']
  const custom = Object.keys(modes).filter(n => !builtIn.includes(n)).sort()
  const modeNames = [...builtIn, ...custom]
  const currentModeKeys = modes[selectedMode] || EMPTY

  return (
    <div className="w-full min-h-screen bg-white flex">
      {/* Side nav */}
      <nav className="w-44 flex-shrink-0 pt-6 pl-6 pr-4 sticky top-0 h-screen">
        <div className="flex flex-col gap-0.5">
          {NAV.map(({ id, label }) => (
            <button
              key={id}
              onClick={() => setSection(id)}
              className={`text-left px-3 py-1.5 rounded-lg text-sm transition-colors ${
                section === id
                  ? 'bg-sidebar-hover-white text-hover-black font-medium'
                  : 'text-normal-black hover:bg-sidebar-hover-white'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </nav>

      {/* Content */}
      <div className="flex-1 max-w-xl py-6 pr-6 pl-2">
        {error && <p className="text-xs text-red mb-4">{error}</p>}

        {section === 'general' && (
          <div className="space-y-5">
            <div className="flex items-center justify-between">
              <span className="text-sm text-normal-black">Dark mode</span>
              <Toggle on={dark} onClick={toggle} />
            </div>

            <div className="flex items-center justify-between">
              <div>
                <span className="text-sm text-normal-black">Execute key fallback</span>
                <p className="text-xs text-normal-black opacity-60 mt-0.5">When chat key is rate-limited, retry with execute mode keys</p>
              </div>
              <Toggle
                on={executeFallback}
                onClick={() => {
                  const next = !executeFallback
                  setExecuteFallback(next)
                  api.executeFallbackSet(next).catch(() => setExecuteFallback(!next))
                }}
              />
            </div>

            <div>
              <label className="block text-xs text-normal-black mb-1">Shell command mode</label>
              <select
                value={shellMode}
                onChange={e => { setShellMode(e.target.value); api.shellModeSet(e.target.value).catch(() => {}) }}
                className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
              >
                <option value="allowlist">Allowlist only</option>
                <option value="prompt">Prompt for approval</option>
                <option value="allow_all">Allow all commands</option>
              </select>
              <p className="text-xs text-normal-black mt-1 opacity-60">Controls how non-allowlisted shell commands are handled</p>
              {shellMode === 'prompt' && shellApproved.length > 0 && (
                <div className="mt-2">
                  <span className="text-xs text-normal-black opacity-60">Permanently approved commands:</span>
                  <div className="flex flex-wrap gap-1.5 mt-1">
                    {shellApproved.map(cmd => (
                      <span key={cmd} className="inline-flex items-center gap-1 px-2 py-0.5 rounded-lg bg-sidebar-white text-xs text-hover-black border border-border-white">
                        <code>{cmd}</code>
                        <button
                          onClick={() => {
                            const next = shellApproved.filter(c => c !== cmd)
                            setShellApproved(next)
                            api.shellApprovedSet(next).catch(() => {})
                          }}
                          className="text-normal-black opacity-40 hover:opacity-100"
                        >&times;</button>
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        )}

        {section === 'api-keys' && (
          <div className="space-y-5">
            {/* Mode tabs */}
            <div className="flex gap-2">
              {modeNames.map(name => (
                <button
                  key={name}
                  onClick={() => { setSelectedMode(name); switchMode(name) }}
                  className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                    activeMode === name
                      ? 'bg-brand-primary text-white'
                      : selectedMode === name
                        ? 'bg-sidebar-hover-white text-hover-black'
                        : 'bg-sidebar-white text-normal-black hover:bg-sidebar-hover-white'
                  }`}
                >
                  {name}
                </button>
              ))}
            </div>

            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <span className="text-xs text-normal-black opacity-60">
                  {selectedMode === 'default' ? 'Main API keys' : `Keys for ${selectedMode} mode (blank = use main keys)`}
                </span>
                {selectedMode !== 'default' && (
                  <button onClick={() => clearModeKeys(selectedMode)} className="text-xs text-normal-black opacity-60 hover:opacity-100 underline">
                    Clear
                  </button>
                )}
              </div>

              {allModels.length > 0 && (
                <>
                  <div>
                    <label className="block text-xs text-normal-black mb-1">Default model</label>
                    <select
                      value={currentModeKeys.model || ''}
                      onChange={e => updateModeModel(selectedMode, e.target.value)}
                      className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
                    >
                      <option value="">Auto (provider default)</option>
                      {allModels.map(m => (
                        <option key={m.id} value={m.id}>{m.name} ({m.provider})</option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs text-normal-black mb-1">Summarization model</label>
                    <select
                      value={currentModeKeys.summarization_model || ''}
                      onChange={e => updateModeSummarizationModel(selectedMode, e.target.value)}
                      className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
                    >
                      <option value="">None (simple truncation)</option>
                      {allModels.map(m => (
                        <option key={m.id} value={m.id}>{m.name} ({m.provider})</option>
                      ))}
                    </select>
                    <p className="text-xs text-normal-black mt-0.5 opacity-60">Cheap model for condensing old tool outputs</p>
                  </div>
                </>
              )}

              {PROVIDERS.map(({ key, label, placeholder, href, hintLabel }) => (
                <div key={key} className="rounded-xl border border-border-white p-4">
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-sm font-medium text-hover-black">{label}</span>
                    <a href={href} target="_blank" rel="noreferrer" className="text-xs text-normal-black opacity-60 hover:opacity-100 underline">{hintLabel ?? 'Get API key'}</a>
                  </div>
                  <input
                    type="password"
                    value={selectedMode === 'default' ? keys[key] : (currentModeKeys[key] || '')}
                    onChange={e => selectedMode === 'default'
                      ? setKeys(k => ({ ...k, [key]: e.target.value }))
                      : updateModeKey(selectedMode, key, e.target.value)
                    }
                    placeholder={placeholder}
                    className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-sidebar-white text-hover-black font-mono outline-none"
                  />
                </div>
              ))}
            </div>
          </div>
        )}

        {section === 'transports' && (
          <div className="space-y-5">
            {TRANSPORT_GROUPS.map(group => (
              <div key={group.label} className="rounded-xl border border-border-white p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium text-hover-black">{group.label}</span>
                  {group.href && (
                    <a href={group.href} target="_blank" rel="noreferrer" className="text-xs text-normal-black opacity-60 hover:opacity-100 underline">Developer portal</a>
                  )}
                </div>
                {group.fields.map(({ key, label, placeholder, hint, secret }) => (
                  <div key={key}>
                    <label className="block text-xs text-normal-black mb-1">{label}</label>
                    <input
                      type={secret ? 'password' : 'text'}
                      value={transportKeys[key]}
                      onChange={e => setTransportKeys(k => ({ ...k, [key]: e.target.value }))}
                      placeholder={placeholder}
                      className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-sidebar-white text-hover-black font-mono outline-none"
                    />
                    <p className="text-xs text-normal-black mt-0.5 opacity-60">{hint}</p>
                  </div>
                ))}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
