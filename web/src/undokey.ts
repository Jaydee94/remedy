/** What `isUndoKey` needs of a keydown event; `editing` says that the key went to a text field, which has an undo of its own. */
export interface UndoKeyEvent {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
  shiftKey: boolean
  defaultPrevented: boolean
  editing: boolean
}

/** Whether a key press is the shortcut for the Undo of a toast: Ctrl+Z or Cmd+Z, outside a text field. */
export function isUndoKey(e: UndoKeyEvent): boolean {
  if (e.defaultPrevented || e.editing || e.altKey || e.shiftKey) return false
  return (e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z'
}
