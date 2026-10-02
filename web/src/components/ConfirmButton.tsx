import { useState } from 'react'
import { Button } from '@/components/ui/button'

interface Props {
  label: string
  confirmLabel: string
  onConfirm: () => void
  disabled?: boolean
}

/** A destructive action that needs a second click. It disarms itself when it loses focus. */
export default function ConfirmButton({ label, confirmLabel, onConfirm, disabled }: Props) {
  const [armed, setArmed] = useState(false)

  return (
    <Button
      type="button"
      size="sm"
      variant={armed ? 'destructive' : 'outline'}
      disabled={disabled}
      onBlur={() => setArmed(false)}
      onClick={() => {
        if (!armed) {
          setArmed(true)
          return
        }
        setArmed(false)
        onConfirm()
      }}
    >
      {armed ? confirmLabel : label}
    </Button>
  )
}
