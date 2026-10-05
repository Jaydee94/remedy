import { createContext, useContext } from 'react'

export interface ToastApi {
  /** Shows a toast for a few seconds. With `undo` it carries an Undo button; a new toast replaces the one that is showing. */
  show: (text: string, undo?: () => void) => void
}

export const ToastContext = createContext<ToastApi>({ show: () => {} })

export function useToast(): ToastApi {
  return useContext(ToastContext)
}
