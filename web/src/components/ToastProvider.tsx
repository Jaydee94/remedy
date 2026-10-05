import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ToastContext } from '@/toast.ts'

const SHOWN_MS = 4200

interface Toast {
  id: number
  text: string
  undo?: () => void
}

/** Hosts the toast of the shell. It sits above the tab bar on a phone. */
export default function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const lastId = useRef(0)

  const show = useCallback((text: string, undo?: () => void) => {
    clearTimeout(timer.current)
    lastId.current += 1
    setToast({ id: lastId.current, text, undo })
    timer.current = setTimeout(() => setToast(null), SHOWN_MS)
  }, [])

  const dismiss = useCallback(() => {
    clearTimeout(timer.current)
    setToast(null)
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  const value = useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={value}>
      {children}
      {toast && (
        <div
          key={toast.id}
          role="status"
          className="fixed bottom-24 left-1/2 z-50 flex w-max max-w-[calc(100%-2rem)] -translate-x-1/2 animate-rm-in items-center gap-3.5 rounded-full bg-foreground py-2.5 pr-2.5 pl-4.5 text-background shadow-2xl md:bottom-7"
        >
          <span className="font-semibold">{toast.text}</span>
          {toast.undo && (
            <button
              type="button"
              onClick={() => {
                toast.undo?.()
                dismiss()
              }}
              className="flex h-8 items-center rounded-full bg-background px-3.5 font-semibold text-primary outline-none focus-visible:ring-3 focus-visible:ring-primary/50"
            >
              Undo
            </button>
          )}
        </div>
      )}
    </ToastContext.Provider>
  )
}
