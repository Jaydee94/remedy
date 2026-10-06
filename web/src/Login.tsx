import { useId, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import { greeting } from './greeting.ts'
import { isGatewayStatus } from './shell.ts'
import RemedyMark from '@/components/RemedyMark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

interface Props {
  onLoggedIn: () => void
  /** A muted line above the form, for example that the session ended. Not an error. */
  notice?: string
}

export default function Login({ onLoggedIn, notice }: Props) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [hello] = useState(() => greeting(new Date().getHours()))
  const noticeId = useId()

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onLoggedIn()
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401) setError("That isn't the admin password.")
        else if (isGatewayStatus(err.status)) setError("I can't reach the server right now.")
        else setError(err.message)
      }
      else setError('Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-7">
      <div className="flex w-full max-w-100 flex-col gap-7">
        <span className="flex items-center gap-3">
          <RemedyMark size={40} />
          <span className="font-serif text-3xl font-medium tracking-tight">remedy</span>
        </span>
        <div className="flex flex-col gap-2.5">
          <h1 className="font-serif text-4xl leading-tight font-normal tracking-tight">{hello}.</h1>
          <p className="text-muted-foreground">Sign in with the admin password and I'll walk you through what happened.</p>
        </div>
        {notice && (
          <p id={noticeId} role="status" className="text-muted-foreground">
            {notice}
          </p>
        )}
        <form onSubmit={submit} className="flex flex-col gap-2.5">
          <Input
            type="password"
            autoFocus
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Admin password"
            aria-label="Admin password"
            aria-invalid={error !== ''}
            aria-describedby={notice ? noticeId : undefined}
            className="h-13 text-[15px]"
          />
          {error && (
            <span role="alert" className="pl-5 text-[13px] text-destructive">
              {error}
            </span>
          )}
          <Button type="submit" size="lg" className="h-13" disabled={busy || password === ''}>
            Sign in
          </Button>
        </form>
      </div>
    </main>
  )
}
