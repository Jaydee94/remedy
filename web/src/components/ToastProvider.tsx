import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ToastContext } from '@/toast.ts'
import { isUndoKey } from '@/undokey.ts'

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
  const ref = useRef<HTMLDivElement>(null)

  const arm = useCallback(() => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setToast(null), SHOWN_MS)
  }, [])

  const hold = useCallback(() => clearTimeout(timer.current), [])

  // Re-arms the timer only when neither the pointer nor the focus is on the toast. At blur time document.activeElement is not yet
  // the new element, so a blur passes the element the focus moves to.
  const settle = useCallback(
    (focusMovingTo?: EventTarget | null) => {
      const el = ref.current
      const focusInside = el !== null && (focusMovingTo instanceof Node ? el.contains(focusMovingTo) : el.contains(document.activeElement))
      if (el !== null && (el.matches(':hover') || focusInside)) return
      arm()
    },
    [arm],
  )

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

  // Undo is the last control in the tab order, so Ctrl+Z or Cmd+Z undoes too while the toast shows, unless a text field has the key.
  const undo = toast?.undo
  useEffect(() => {
    if (!undo) return
    const onKey = (e: KeyboardEvent) => {
      const el = e.target instanceof HTMLElement ? e.target : null
      const editing = el !== null && (el.isContentEditable || el.matches('input, textarea, select'))
      if (!isUndoKey({ key: e.key, ctrlKey: e.ctrlKey, metaKey: e.metaKey, altKey: e.altKey, shiftKey: e.shiftKey, defaultPrevented: e.defaultPrevented, editing })) return
      e.preventDefault()
      undo()
      dismiss()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [undo, dismiss])

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
            ref={ref}
            onPointerEnter={(e) => {
              if (e.pointerType === 'mouse') hold()
            }}
            onPointerLeave={(e) => {
              if (e.pointerType === 'mouse') settle()
            }}
            onFocus={hold}
            onBlur={(e) => settle(e.relatedTarget)}
            className="pointer-events-auto flex w-max max-w-full animate-rm-in items-center gap-3.5 rounded-full bg-foreground py-2.5 pr-2.5 pl-4.5 text-background shadow-2xl"
          >
            <span className="min-w-0 font-semibold break-words">{toast.text}</span>
            {toast.undo && (
              <button
                type="button"
                aria-keyshortcuts="Control+Z Meta+Z"
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
