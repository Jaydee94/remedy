import { useCallback, useRef } from 'react'

/**
 * The focus return after the control that had it has gone (a button that was replaced by a message, an ask that was answered): the
 * focus falls to the page, so it goes to the element behind `target` (the page heading) when it is on the page or nowhere. A field
 * the user is typing in keeps it.
 */
export function useFocusRestore<T extends HTMLElement>() {
  const target = useRef<T>(null)
  const restore = useCallback(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50)) // let the page render without the control that went away
    const active = document.activeElement
    if (active === null || active === document.body) target.current?.focus({ preventScroll: true })
  }, [])
  return { target, restore }
}
