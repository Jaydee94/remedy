import { useCallback, useRef } from 'react'

const POLL_MS = 100
const GIVE_UP_MS = 3000

const nowhere = (el: Element | null) => el === null || el === document.body

/**
 * The focus return after the control that had it has gone (a button that was replaced by a message, an ask that was answered). The
 * control that has the focus when `restore` is called is the origin; the buttons that start a request keep the focus while they work
 * (`aria-disabled`), so the origin stays until the page has really replaced it. `restore` waits for that (up to three seconds) and then
 * moves the focus to the element behind `target` (the page heading), but only when it has fallen to the page: a user who has moved
 * on, or typed into a field, keeps the focus, and so does a control that stays.
 *
 * Call `restore` BEFORE the reload that removes the control: it captures the focused element when it is called, and once the control
 * has gone that is the page. Await the returned promise after the reload.
 */
export function useFocusRestore<T extends HTMLElement>() {
  const target = useRef<T>(null)
  const restore = useCallback(async () => {
    const origin = document.activeElement
    if (origin === null || origin === document.body) return // nothing had the focus: nothing to bring back
    for (let waited = 0; waited < GIVE_UP_MS; waited += POLL_MS) {
      await new Promise((resolve) => setTimeout(resolve, POLL_MS))
      const active = document.activeElement
      if (!origin.isConnected) {
        if (nowhere(active)) target.current?.focus({ preventScroll: true })
        return
      }
      if (active !== origin && !nowhere(active)) return // the user moved the focus elsewhere
    }
  }, [])
  return { target, restore }
}
