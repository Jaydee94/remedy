import type { Host, Skill } from '@/api.ts'
import { originText } from '@/skills.ts'
import SkillForm from '@/components/skills/SkillForm'
import type { SkillValues } from '@/components/skills/SkillForm'
import ConfirmButton from '@/components/ConfirmButton'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'

interface Props {
  skill: Skill
  hosts: Host[]
  busy: boolean
  editing: boolean
  onToggle: (on: boolean) => void
  onEdit: () => void
  onCancelEdit: () => void
  onSave: (values: SkillValues) => void
  onDelete: () => void
}

/** One skill of the list: its name, whether it is on, what it is about, who wrote it, and its note as text on demand. */
export default function SkillRow({ skill, hosts, busy, editing, onToggle, onEdit, onCancelEdit, onSave, onDelete }: Props) {
  if (editing) {
    return (
      <li className="rounded-3xl border border-border bg-card p-5">
        <SkillForm hosts={hosts} skill={skill} busy={busy} submitLabel="Save" onSubmit={onSave} onCancel={onCancelEdit} />
      </li>
    )
  }
  const on = skill.state === 'active'
  return (
    <li className="flex flex-col gap-2 rounded-3xl border border-border bg-card p-5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <strong className="font-mono text-[15px] break-all">{skill.name}</strong>
        {!on && <span className="rounded-2xl bg-muted px-2.5 py-0.5 text-xs font-semibold text-muted-foreground">Off</span>}
        <span className="ml-auto flex items-center gap-2 text-[13px] text-muted-foreground">
          <Switch
            checked={on}
            aria-label={on ? `Turn ${skill.name} off` : `Turn ${skill.name} on`}
            onCheckedChange={(v: boolean) => {
              if (!busy) onToggle(v)
            }}
          />
          <span aria-hidden>{on ? 'On' : 'Off'}</span>
        </span>
      </div>
      <p className="break-words">{skill.description}</p>
      <span className="text-[13px] text-muted-foreground">{originText(skill)}</span>
      <details className="text-[13px]">
        <summary className="cursor-pointer text-muted-foreground">Read the note</summary>
        <pre className="mt-2 rounded-xl border border-border bg-background px-3 py-2 font-mono text-[13px] whitespace-pre-wrap break-words">{skill.body}</pre>
      </details>
      <div className="flex flex-wrap gap-2 pt-1">
        <Button type="button" variant="outline" size="sm" className="h-9" onClick={onEdit}>
          Edit
        </Button>
        <ConfirmButton label="Delete" confirmLabel="Confirm: delete" className="h-9" disabled={busy} onConfirm={onDelete} />
      </div>
    </li>
  )
}
