import { useEffect, useState } from 'react'
import { api, type UsageSummary, type CreditEntry, type ProviderKeys } from '@/lib/api'

// Pricing per million tokens (USD): [input, output]
const PRICING: Record<string, [number, number]> = {
  // Anthropic
  'claude-sonnet-4-20250514': [3, 15],
  'claude-sonnet-4-6': [3, 15],
  'claude-haiku-4-20250414': [0.80, 4],
  'claude-haiku-4-5-20251001': [0.80, 4],
  'claude-opus-4-20250514': [15, 75],
  'claude-opus-4-6': [15, 75],
  // OpenAI
  'gpt-4o': [2.50, 10],
  'gpt-4o-mini': [0.15, 0.60],
  'gpt-4.1': [2, 8],
  'gpt-4.1-mini': [0.40, 1.60],
  'gpt-4.1-nano': [0.10, 0.40],
  'gpt-4-turbo': [10, 30],
  'o3': [2, 8],
  'o3-mini': [1.10, 4.40],
  'o4-mini': [1.10, 4.40],
  // Gemini
  'gemini-2.5-pro': [1.25, 10],
  'gemini-2.5-pro-preview-05-06': [1.25, 10],
  'gemini-2.5-flash': [0.15, 0.60],
  'gemini-2.5-flash-preview-04-17': [0.15, 0.60],
  'gemini-2.0-flash': [0.10, 0.40],
  'gemini-2.0-flash-001': [0.10, 0.40],
  'gemini-3-flash-preview': [0.10, 0.40],
  // OpenRouter
  'anthropic/claude-sonnet-4-6': [3, 15],
  'anthropic/claude-opus-4-6': [15, 75],
  'openai/gpt-4o': [2.50, 10],
  'openai/gpt-4o-mini': [0.15, 0.60],
  'google/gemini-2.0-flash-001': [0.10, 0.40],
}

function cost(model: string, inputTokens: number, outputTokens: number): number {
  const p = PRICING[model]
  if (!p) return 0
  return (inputTokens * p[0] + outputTokens * p[1]) / 1_000_000
}

function fmt(usd: number): string {
  if (usd === 0) return '$0.00'
  if (usd < 0.01) return `$${usd.toFixed(4)}`
  return `$${usd.toFixed(2)}`
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

type Period = '24h' | '7d' | '30d' | 'all'
type ModeFilter = 'total' | 'default' | 'chat' | 'execute'

function sinceFor(period: Period): string | undefined {
  if (period === 'all') return undefined
  const ms = { '24h': 86400000, '7d': 604800000, '30d': 2592000000 }[period]
  return new Date(Date.now() - ms).toISOString()
}

const PROVIDER_KEYS = ['anthropic', 'openai', 'gemini', 'openrouter'] as const

export function CostsPage() {
  const [rows, setRows] = useState<UsageSummary[]>([])
  const [credits, setCredits] = useState<CreditEntry[]>([])
  const [keys, setKeys] = useState<ProviderKeys | null>(null)
  const [period, setPeriod] = useState<Period>('30d')
  const [mode, setMode] = useState<ModeFilter>('total')

  useEffect(() => {
    api.usage(sinceFor(period)).then(setRows).catch(() => {})
  }, [period])

  useEffect(() => {
    api.credits().then(setCredits).catch(() => {})
    api.configKeys().then(setKeys).catch(() => {})
  }, [])

  // Filter by mode
  const filtered = mode === 'total' ? rows : rows.filter(r => r.mode === mode)

  // Which providers have keys configured
  const configured = PROVIDER_KEYS.filter(p => keys && keys[p])

  // Group filtered rows by provider
  const byProvider: Record<string, UsageSummary[]> = {}
  for (const r of filtered) {
    const key = r.provider || 'unknown'
    ;(byProvider[key] ||= []).push(r)
  }

  // Credits lookup
  const creditMap: Record<string, CreditEntry> = {}
  for (const c of credits) creditMap[c.provider] = c

  const totalCost = filtered.reduce((s, r) => s + cost(r.model, r.input_tokens, r.output_tokens), 0)

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto space-y-6">
        {/* Period + Mode selectors */}
        <div className="flex gap-6">
          <div className="flex gap-2">
            {(['24h', '7d', '30d', 'all'] as Period[]).map(p => (
              <button
                key={p}
                onClick={() => setPeriod(p)}
                className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                  period === p
                    ? 'bg-brand-primary text-white'
                    : 'bg-sidebar-white text-normal-black hover:bg-sidebar-hover-white'
                }`}
              >
                {p === 'all' ? 'All time' : p}
              </button>
            ))}
          </div>
          <div className="flex gap-2">
            {(['total', 'default', 'chat', 'execute'] as ModeFilter[]).map(m => (
              <button
                key={m}
                onClick={() => setMode(m)}
                className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                  mode === m
                    ? 'bg-brand-primary text-white'
                    : 'bg-sidebar-white text-normal-black hover:bg-sidebar-hover-white'
                }`}
              >
                {m === 'total' ? 'Total' : m}
              </button>
            ))}
          </div>
        </div>

        {/* Total */}
        <div className="p-4 rounded-xl bg-sidebar-white">
          <p className="text-xs text-normal-black opacity-60 mb-1">
            {mode === 'total' ? 'All modes' : `${mode} mode`}
          </p>
          <p className="text-2xl text-hover-black font-medium">{fmt(totalCost)}</p>
        </div>

        {/* Per-key breakdown */}
        {configured.length === 0 && rows.length === 0 && (
          <p className="text-sm text-normal-black opacity-60">No API keys configured. Add keys in Settings to start tracking costs.</p>
        )}

        {configured.map(provider => {
          const providerRows = byProvider[provider] || []
          const providerCost = providerRows.reduce((s, r) => s + cost(r.model, r.input_tokens, r.output_tokens), 0)
          const providerInput = providerRows.reduce((s, r) => s + r.input_tokens, 0)
          const providerOutput = providerRows.reduce((s, r) => s + r.output_tokens, 0)
          const credit = creditMap[provider]

          return (
            <div key={provider} className="rounded-xl bg-sidebar-white overflow-hidden">
              <div className="p-4 flex items-center justify-between">
                <div>
                  <p className="text-sm text-hover-black font-medium capitalize">{provider}</p>
                  <p className="text-xs text-normal-black opacity-60 font-mono">
                    {keys?.[provider]?.replace(/(.{6}).*(.{4})/, '$1...$2')}
                  </p>
                </div>
                <div className="text-right">
                  {credit && mode === 'total' ? (
                    <>
                      <p className="text-lg text-hover-black font-medium">{fmt(credit.total_used_usd)}</p>
                      {credit.limit_usd > 0 && (
                        <p className="text-xs text-normal-black opacity-60">
                          of {fmt(credit.limit_usd)} limit
                        </p>
                      )}
                    </>
                  ) : (
                    <>
                      <p className="text-lg text-hover-black font-medium">{fmt(providerCost)}</p>
                      {(providerInput > 0 || providerOutput > 0) && (
                        <p className="text-xs text-normal-black opacity-60">
                          {fmtTokens(providerInput)}↑ {fmtTokens(providerOutput)}↓
                        </p>
                      )}
                    </>
                  )}
                </div>
              </div>

              {providerRows.length > 0 && (
                <div className="border-t border-border-white">
                  {providerRows
                    .sort((a, b) => cost(b.model, b.input_tokens, b.output_tokens) - cost(a.model, a.input_tokens, a.output_tokens))
                    .map((r, i) => (
                    <div key={i} className="px-4 py-1.5 flex items-center justify-between border-b border-border-white last:border-0">
                      <span className="text-xs text-normal-black opacity-80 truncate mr-2">
                        {r.model || 'unknown model'}
                      </span>
                      <span className="text-xs text-normal-black opacity-60 whitespace-nowrap">
                        {fmtTokens(r.input_tokens)}↑ {fmtTokens(r.output_tokens)}↓ · {fmt(cost(r.model, r.input_tokens, r.output_tokens))}
                      </span>
                    </div>
                  ))}
                </div>
              )}

              {providerRows.length === 0 && !credit && (
                <div className="border-t border-border-white px-4 py-3">
                  <p className="text-xs text-normal-black opacity-40">No usage recorded{mode !== 'total' ? ` in ${mode} mode` : ''}</p>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
