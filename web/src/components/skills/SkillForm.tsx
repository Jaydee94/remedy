import { useId, useState } from 'react'
import type { FormEvent } from 'react'
import type { Host, Skill } from '@/api.ts'
import { MAX_BODY_BYTES, MAX_DESCRIPTION, byteLength, normalizeBody, skillProblem } from '@/skills.ts'
import Field from '@/components/hosts/Field'
import { ariaDisabled } from '@/components/setup/ariaDisabled.ts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

export interface SkillValues {
  hostId: number | null
  name: string
  description: string
  body: string
}

interface Props {
  hosts: Host[]
  /** A skill to change: the form starts with its texts and its scope cannot change. */
  skill?: Skill
  busy: boolean
  submitLabel: string
  onSubmit: (values: SkillValues) => void
  onCancel: () => void
}

const field = 'h-11 text-base md:text-sm'
const area = 'rounded-xl border border-input bg-transparent px-3 py-2 text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm'

/** The texts of a skill. A skill is plain text: the body is shown as text everywhere, Markdown is not rendered. */
export default function SkillForm({ hosts, skill, busy, submitLabel, onSubmit, onCancel }: Props) {
  const [hostId, setHostId] = useState(skill ? (skill.hostId === null ? '' : String(skill.hostId)) : '')
  const [name, setName] = useState(skill?.name ?? '')
  const [description, setDescription] = useState(skill?.description ?? '')
  const [body, setBody] = useState(skill?.body ?? '')
  const [shown, setShown] = useState(false)
  const problemId = useId()
  const problem = skillProblem({ name, description, body })

  function submit(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    if (problem) {
      setShown(true) // the button stays clickable so that a keyboard user hears what is wrong
      return
    }
    onSubmit({ hostId: hostId === '' ? null : Number(hostId), name: name.trim(), description: description.trim(), body })
  }

  const kb = (byteLength(normalizeBody(body)) / 1024).toFixed(1)
  return (
    <form onSubmit={submit} className="flex flex-col gap-4" aria-describedby={shown && problem ? problemId : undefined}>
      <Field label="Where it applies" hint={skill ? 'The scope of a skill does not change.' : "Global skills are listed for every run; a host's skills only for runs that have the host."}>
        <select value={hostId} onChange={(e) => setHostId(e.target.value)} disabled={skill !== undefined} autoFocus={skill === undefined} className={cn(area, 'h-11')}>
          <option value="">Global</option>
          {hosts.map((h) => (
            <option key={h.id} value={String(h.id)}>
              {h.name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Name" hint="Lower case letters, digits and dashes. The agent passes it to skill_get.">
        <Input value={name} onChange={(e) => setName(e.target.value)} className={field} autoComplete="off" spellCheck={false} autoFocus={skill !== undefined} />
      </Field>
      <Field label="Description" hint={`One line that says what the note is about. ${[...description.trim()].length} of ${MAX_DESCRIPTION} characters.`}>
        <Input value={description} onChange={(e) => setDescription(e.target.value)} className={field} autoComplete="off" />
      </Field>
      <Field label="Note" hint={`Plain text; Markdown is shown as it is typed. ${kb} KB of ${MAX_BODY_BYTES / 1024} KB.`}>
        <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={8} className={cn(area, 'min-h-40 font-mono')} />
      </Field>
      {shown && problem && (
        <p id={problemId} role="alert" className="text-[13px] text-destructive">
          {problem}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" aria-disabled={busy} className={cn('h-11', ariaDisabled)}>
          {submitLabel}
        </Button>
        <Button type="button" variant="ghost" className="h-11" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  )
}
