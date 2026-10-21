import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Host, PresentedKey, Skill } from './api.ts'
import { editableKey, statusSentence } from './hosts.ts'
import { nextHost } from './learn.ts'
import { deleteHostLabel, hostDeleteWarning } from './skills.ts'
import { useFocusRestore } from './useFocusRestore.ts'
import ConfirmButton from '@/components/ConfirmButton'
import HostForm from '@/components/hosts/HostForm'
import LearnSection from '@/components/hosts/LearnSection'
import LoginSection from '@/components/hosts/LoginSection'
import RightsSection from '@/components/hosts/RightsSection'
import type { Run } from '@/components/hosts/run'
import SnippetSection from '@/components/hosts/SnippetSection'
import TrustSection from '@/components/hosts/TrustSection'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

function Part({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">{title}</h2>
      {children}
    </section>
  )
}

/** One host: the form, how I log in, whether I trust its key, what the account can do, and what to run on the host. */
export default function HostPage({ id }: { id: number }) {
  const navigate = useNavigate()
  const [host, setHost] = useState<Host | null>(null)
  const [presented, setPresented] = useState<PresentedKey | null>(null)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [gone, setGone] = useState(false)
  const [skills, setSkills] = useState<Skill[] | null>(null)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()

  useEffect(() => {
    api
      .getHost(id)
      .then(setHost)
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) setGone(true)
        else setError(e instanceof ApiError ? e.message : 'Could not load the host')
      })
  }, [id])

  // What deleting the host takes with it: its skills and the proposals that wait for a review. Not knowing is shown as such.
  // `stale` keeps a slow answer for the previous host from replacing the one of the current host.
  useEffect(() => {
    let stale = false
    api
      .listSkills({ host: id })
      .then((list) => {
        if (!stale) setSkills(list)
      })
      .catch(() => {
        if (!stale) setSkills(null)
      })
    return () => {
      stale = true
    }
  }, [id])

  // One request at a time. A 404 means the host was removed elsewhere (another tab): say so once, instead of an error per button.
  const run: Run = useCallback(async <T,>(action: () => Promise<T>) => {
    if (busyRef.current) return undefined
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      return await action()
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setGone(true)
      else setError(e instanceof ApiError ? e.message : 'Request failed')
      return undefined
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }, [])

  // The host and its skills again: while a learning run is going, and after it was started. A poll that fails keeps what the page has.
  const refresh = useCallback(async () => {
    const [h, s] = await Promise.allSettled([api.getHost(id), api.listSkills({ host: id })])
    setHost((prev) => (prev ? nextHost(prev, h) : prev))
    if (s.status === 'fulfilled') setSkills(s.value)
  }, [id])

  // For the actions that remove the control that was clicked (pin, unpin, save and remove a login): the focus goes back to the heading.
  // `restore` remembers the focused control when it is called, so it comes first; it does nothing when the control stays (a failure).
  const runRestoring: Run = useCallback(
    <T,>(action: () => Promise<T>) => {
      void restore()
      return run(action)
    },
    [restore, run],
  )

  async function remove() {
    if ((await run(() => api.deleteHost(id).then(() => true))) === true) navigate('/hosts')
  }

  if (gone) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-4 px-4 py-5 md:px-10 md:py-12">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">This host is gone</h1>
        <p className="text-muted-foreground">It was removed, maybe in another window.</p>
        <Link to="/hosts" className="text-primary underline">
          Back to the hosts
        </Link>
      </div>
    )
  }

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      {host ? (
        <>
          <div className="flex flex-col gap-1">
            <h1 ref={heading} tabIndex={-1} className="font-serif text-[clamp(26px,3vw,34px)] font-normal break-all outline-none">
              {host.name}
            </h1>
            <span className="text-muted-foreground">{statusSentence(host)}</span>
          </div>

          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          {notice && (
            <Alert role="status">
              <AlertDescription>{notice}</AlertDescription>
            </Alert>
          )}

          <Part title="The host">
            <HostForm
              key={editableKey(host)}
              host={host}
              busy={busy}
              run={run}
              onSaved={(h, pinCleared) => {
                setHost(h)
                setNotice(pinCleared ? 'The address changed, so I forgot the pinned key. Test the connection and pin it again.' : '')
                setPresented(null) // a key shown for the old address or port must not be offered for the new one
              }}
            />
          </Part>
          <Part title="How I log in">
            <LoginSection
              key={host.credential?.updatedAt ?? 'none'}
              host={host}
              busy={busy}
              run={runRestoring}
              onChanged={(h, text) => {
                setHost(h)
                setNotice(text)
              }}
            />
          </Part>
          <Part title="Trust">
            <TrustSection host={host} presented={presented} busy={busy} run={runRestoring} onHost={setHost} onPresented={setPresented} />
          </Part>
          <Part title="What the account can do">
            {host.credential ? (
              <RightsSection
                host={host}
                busy={busy}
                run={run}
                onTested={(h, key) => {
                  setHost(h)
                  setPresented(key)
                  setNotice('')
                }}
              />
            ) : (
              <p className="text-muted-foreground">Give me a login first.</p>
            )}
          </Part>
          <Part title="Get to know this host">
            <LearnSection host={host} skills={skills} busy={busy} run={run} onRefresh={refresh} onNotice={setNotice} />
          </Part>
          <Part title="Set the account up">
            <SnippetSection host={host} />
          </Part>

          <div className="flex flex-col gap-3 border-t border-border pt-4">
            {skills !== null && hostDeleteWarning(skills) && <p className="text-[13px] text-muted-foreground">{hostDeleteWarning(skills)}</p>}
            <div>
              <ConfirmButton label="Delete host" confirmLabel={deleteHostLabel(skills)} className="h-10" disabled={busy} onConfirm={() => void remove()} />
            </div>
          </div>
        </>
      ) : (
        !error && <Skeleton className="h-64" />
      )}
      {!host && error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}
