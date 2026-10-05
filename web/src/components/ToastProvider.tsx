import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ToastContext } from '@/toast.ts'

const SHOWN_MS = 4200

interface Toast {
  id: number
  text: string
  undo?: () => void
}

/**
 * Hosts the toast of the shell. It sits above the tab bar on a phone. The live region is always in the page, so that a screen reader
 * announces a toast that is put into it; the timer stops while the pointer or the focus is on the toast (WCAG 2.2.1).
 */
export default function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const lastId = useRef(0)

  const arm = useCallback(() => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setToast(null), SHOWN_MS)
  }, [])

  const hold = useCallback(() => clearTimeout(timer.current), [])

  const show = useCallback(
    (text: string, undo?: () => void) => {
      lastId.current += 1
      setToast({ id: lastId.current, text, undo })
      arm()
    },
    [arm],
  )

  const dismiss = useCallback(() => {
    clearTimeout(timer.current)
    setToast(null)
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  const value = useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed bottom-24 left-1/2 z-50 flex w-max max-w-[calc(100%-2rem)] -translate-x-1/2 justify-center md:bottom-7"
      >
        {toast && (
          <div
            key={toast.id}
            onMouseEnter={hold}
            onMouseLeave={arm}
            onFocus={hold}
            onBlur={arm}
            className="pointer-events-auto flex w-max max-w-full animate-rm-in items-center gap-3.5 rounded-full bg-foreground py-2.5 pr-2.5 pl-4.5 text-background shadow-2xl"
          >
            <span className="min-w-0 font-semibold break-words">{toast.text}</span>
            {toast.undo && (
              <button
                type="button"
                onClick={() => {
                  toast.undo?.()
                  dismiss()
                }}
                className="flex h-8 shrink-0 items-center rounded-full bg-background px-3.5 font-semibold text-primary outline-none focus-visible:ring-3 focus-visible:ring-primary/50"
              >
                Undo
              </button>
            )}
          </div>
        )}
      </div>
    </ToastContext.Provider>
  )
}
