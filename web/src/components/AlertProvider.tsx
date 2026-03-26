/* eslint-disable react-refresh/only-export-components */
import React, { createContext, useContext, useState, useCallback } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { motion, AnimatePresence } from 'framer-motion'
import { X, AlertCircle, CheckCircle, Info, AlertTriangle } from 'lucide-react'

export const SILENCE_ERRORS_KEY = 'silence-errors'

interface Alert {
  id: string
  message: string
  type?: 'info' | 'success' | 'warning' | 'error'
  dismissKey?: string
}

interface Confirm {
  id: string
  message: string
  onConfirm: () => void
  onCancel: () => void
  hideCancel?: boolean
}

interface AlertContextType {
  alert: (message: string, type?: Alert['type'], dismissKey?: string) => void
  confirm: (message: string, hideCancel?: boolean) => Promise<boolean>
}

const AlertContext = createContext<AlertContextType | undefined>(undefined)

export const useAlert = () => {
  const ctx = useContext(AlertContext)
  if (!ctx) throw new Error('useAlert must be used within AlertProvider')
  return ctx
}

export const AlertProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [confirms, setConfirms] = useState<Confirm[]>([])

  const alert = useCallback((message: string, type: Alert['type'] = 'info', dismissKey?: string) => {
    if (type === 'error' && localStorage.getItem(SILENCE_ERRORS_KEY) === 'true') return
    if (dismissKey && localStorage.getItem(dismissKey)) return
    const id = `alert-${Date.now()}-${Math.random()}`
    setAlerts(prev => [...prev, { id, message, type, dismissKey }])
  }, [])

  const confirm = useCallback((message: string, hideCancel = false): Promise<boolean> => {
    return new Promise(resolve => {
      const id = `confirm-${Date.now()}-${Math.random()}`
      setConfirms(prev => [...prev, {
        id, message, hideCancel,
        onConfirm: () => { setConfirms(p => p.filter(c => c.id !== id)); resolve(true) },
        onCancel:  () => { setConfirms(p => p.filter(c => c.id !== id)); resolve(false) },
      }])
    })
  }, [])

  const removeAlert = useCallback((id: string) => {
    setAlerts(prev => prev.filter(a => a.id !== id))
  }, [])

  const getIcon = (type: Alert['type']) => {
    switch (type) {
      case 'success': return <CheckCircle className="w-5 h-5 text-terminal-green shrink-0" />
      case 'warning': return <AlertTriangle className="w-5 h-5 text-yellow-500 shrink-0" />
      case 'error':   return <AlertCircle className="w-5 h-5 text-red shrink-0" />
      default:        return <Info className="w-5 h-5 text-brand-primary shrink-0" />
    }
  }

  return (
    <AlertContext.Provider value={{ alert, confirm }}>
      {children}
      {createPortal(
        <>
          <div className="fixed top-4 right-4 z-[9999] flex flex-col gap-2 max-w-md">
            <AnimatePresence>
              {alerts.map(a => (
                <motion.div
                  key={a.id}
                  initial={{ opacity: 0, y: -20, scale: 0.95 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0, y: -20, scale: 0.95 }}
                  className="bg-white border border-border-white rounded-2xl p-4 shadow-lg"
                >
                  <div className="flex items-start gap-3">
                    {getIcon(a.type)}
                    <div className="flex-1 text-sm text-hover-black">{a.message}</div>
                    <button onClick={() => removeAlert(a.id)} className="text-normal-black hover:text-hover-black transition-colors shrink-0">
                      <X className="w-4 h-4" />
                    </button>
                  </div>
                  {a.dismissKey && (
                    <div className="mt-3 pt-3 border-t border-border-white">
                      <button
                        onClick={() => { localStorage.setItem(a.dismissKey!, 'true'); removeAlert(a.id) }}
                        className="text-xs text-normal-black hover:text-hover-black transition-colors"
                      >
                        Don't show this again
                      </button>
                    </div>
                  )}
                </motion.div>
              ))}
            </AnimatePresence>
          </div>

          <AnimatePresence>
            {confirms.map(c => (
              <motion.div
                key={c.id}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/40 backdrop-blur-sm"
                onClick={c.onCancel}
              >
                <motion.div
                  initial={{ scale: 0.95, opacity: 0 }}
                  animate={{ scale: 1, opacity: 1 }}
                  exit={{ scale: 0.95, opacity: 0 }}
                  className="bg-white border border-border-white rounded-2xl p-6 max-w-md w-full mx-4 shadow-xl"
                  onClick={e => e.stopPropagation()}
                >
                  <div className="text-sm text-hover-black mb-6 whitespace-pre-line">{c.message}</div>
                  <div className="flex gap-4 justify-end items-center">
                    {!c.hideCancel && (
                      <button type="button" onClick={c.onCancel} className="text-sm text-normal-black hover:text-hover-black transition-colors">
                        Cancel
                      </button>
                    )}
                    <button
                      type="button"
                      onClick={c.onConfirm}
                      className="px-4 py-2 text-sm bg-brand-primary text-white rounded-xl hover:opacity-80 transition-opacity"
                    >
                      Confirm
                    </button>
                  </div>
                </motion.div>
              </motion.div>
            ))}
          </AnimatePresence>
        </>,
        document.body
      )}
    </AlertContext.Provider>
  )
}
