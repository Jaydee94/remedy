import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { Host, Skill } from './api.ts'
import { confirmBlocked, reviewItems, scopeGroups, skillNotice } from './skills.ts'
import type { SkillAction } from './skills.ts'
import { useFocusRestore } from './useFocusRestore.ts'
import ReviewCard from '@/components/skills/ReviewCard'
import SkillForm from '@/components/skills/SkillForm'
import type { SkillValues } from '@/components/skills/SkillForm'
import SkillRow from '@/components/skills/SkillRow'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

/** What I know about the lab: the proposals that wait for a review, and every skill by scope. */
export default function SkillsPage() {
  const [skills, setSkills] = useState<Skill[] | null>(null)
  const [hosts, setHosts] = useState<Host[]>([])
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<number | null>(null)
  const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()

  // Not an async function: oxlint reads a state setter in the body of an async function that an effect calls as a synchronous one.
  const reload = useCallback(
    () =>
      Promise.all([api.listSkills(), api.listHosts().catch(() => [] as Host[])])
        .then(([list, hostList]) => {
          setSkills(list)
          setHosts(hostList)
          setLoadError('')
        })
        .catch((e: unknown) => setLoadError(e instanceof ApiError ? e.message : 'Could not load the skills')),
    [],
  )

  useEffect(() => {
    void reload()
  }, [reload])

  /**
   * One request at a time. The page reloads after it, also after a failure, so that it shows what is really there (a proposal that
   * another window confirmed). `restoring` is for an action that removes the control that was clicked: the focus then returns to the
   * heading. Returns whether the action worked.
   */
  const act = useCallback(
    async (action: () => Promise<unknown>, done: string, restoring = false): Promise<boolean> => {
      if (busyRef.current) return false
      busyRef.current = true
      setBusy(true)
      setError('')
      setNotice('')
      if (restoring) void restore()
      let ok = false
      try {
        await action()
        ok = true
        setNotice(done)
      } catch (e) {
        setError(e instanceof ApiError ? e.message : 'Request failed')
      } finally {
        await reload()
        busyRef.current = false
        setBusy(false)
      }
      return ok
    },
    [reload, restore],
  )

  const say = (action: SkillAction, name: string) => skillNotice(action, name)
  const items = skills ? reviewItems(skills) : []
  const groups = skills ? scopeGroups(skills) : []

  async function create(v: SkillValues) {
    if (await act(() => api.createSkill({ hostId: v.hostId, name: v.name, description: v.description, body: v.body }), say('created', v.name), true)) setAdding(false)
  }

  async function save(s: Skill, v: SkillValues) {
    if (await act(() => api.patchSkill(s.id, { name: v.name, description: v.description, body: v.body }), say('saved', v.name), true)) setEditing(null)
  }

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 ref={heading} tabIndex={-1} className="font-serif text-[clamp(26px,3vw,34px)] font-normal outline-none">
          Skills
        </h1>
        <span className="text-muted-foreground">
          What I know about your lab. I list the notes that fit a run and load one when its description matches the question. A note informs me; it never
          gives me a permission.
        </span>
      </div>

      {(loadError || error) && (
        <Alert variant="destructive">
          <AlertDescription>{error || loadError}</AlertDescription>
        </Alert>
      )}
      <div role="status" aria-live="polite" className="text-[13px] text-muted-foreground empty:hidden">
        {notice}
      </div>

      {skills === null && !loadError && <Skeleton className="h-48" />}

      {skills !== null && (
        <>
          <section className="flex flex-col gap-3.5" aria-labelledby="review-title">
            <h2 id="review-title" className="text-xs font-semibold text-muted-foreground">
              Waiting for your review{items.length > 0 ? ` (${items.length})` : ''}
            </h2>
            {items.length === 0 ? (
              <p className="font-serif text-[17px] leading-relaxed text-pretty">Nothing waits for your review. What an agent proposes about a host waits here until you confirm it.</p>
            ) : (
              <ul className="flex flex-col gap-3">
                {items.map((item) => (
                  <ReviewCard
                    key={item.skill.id}
                    item={item}
                    blocked={confirmBlocked(skills, item.skill)}
                    busy={busy}
                    onConfirm={(edit) => void act(() => api.confirmSkill(item.skill.id, edit), say('confirmed', item.skill.name), true)}
                    onReject={() => void act(() => api.rejectSkill(item.skill.id), say('rejected', item.skill.name), true)}
                  />
                ))}
              </ul>
            )}
          </section>

          <section className="flex flex-col gap-3.5" aria-labelledby="all-title">
            <div className="flex items-center gap-3">
              <h2 id="all-title" className="text-xs font-semibold text-muted-foreground">
                All skills
              </h2>
              {!adding && (
                <Button type="button" variant="outline" size="sm" className="ml-auto h-9" onClick={() => setAdding(true)}>
                  New skill
                </Button>
              )}
            </div>
            {adding && (
              <div className="rounded-3xl border border-border bg-card p-5">
                <SkillForm hosts={hosts} busy={busy} submitLabel="Save" onSubmit={(v) => void create(v)} onCancel={() => setAdding(false)} />
              </div>
            )}
            {groups.length === 0 && !adding && (
              <p className="font-serif text-[17px] leading-relaxed text-pretty">
                I have no notes yet. Write one about a machine or a habit of your lab, for example where a service keeps its data, and I will list it for the runs it fits.
              </p>
            )}
            {groups.map((group) => (
              <div key={group.key} className="flex flex-col gap-2.5">
                <h3 className="text-[13px] font-semibold">{group.title}</h3>
                <ul className="flex flex-col gap-3" aria-label={`Skills of ${group.title}`}>
                  {group.skills.map((s) => (
                    <SkillRow
                      key={s.id}
                      skill={s}
                      hosts={hosts}
                      busy={busy}
                      editing={editing === s.id}
                      onToggle={(on) => void act(() => api.patchSkill(s.id, { state: on ? 'active' : 'off' }), say(on ? 'on' : 'off', s.name))}
                      onEdit={() => setEditing(s.id)}
                      onCancelEdit={() => setEditing(null)}
                      onSave={(v) => void save(s, v)}
                      onDelete={() => void act(() => api.deleteSkill(s.id), say('deleted', s.name), true)}
                    />
                  ))}
                </ul>
              </div>
            ))}
          </section>
        </>
      )}
    </div>
  )
}
