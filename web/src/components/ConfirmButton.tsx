import { useState } from 'react'
import { Button } from '@/components/ui/button'

interface Props {
  label: string
  confirmLabel: string
  onConfirm: () => void
  disabled?: boolean
  className?: string
}

/** A destructive action that needs a second click. It disarms itself when it loses focus. */
export default function ConfirmButton({ label, confirmLabel, onConfirm, disabled, className }: Props) {
  const [armed, setArmed] = useState(false)

  return (
    <Button
      type="button"
      size="sm"
      variant={armed ? 'destructive' : 'outline'}
      disabled={disabled}
      className={className}
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
