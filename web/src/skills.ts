import type { Skill } from './api.ts'

export const MAX_DESCRIPTION = 200
export const MAX_BODY_BYTES = 8192
export const MAX_PER_SCOPE = 30

export const validSkillName = (s: string) => /^[a-z0-9][a-z0-9-]{0,39}$/.test(s.trim())

const encoder = new TextEncoder()
export const byteLength = (s: string) => encoder.encode(s).length

/**
 * The first rule a skill form breaks, in the words of the API, or '' when the server can be asked. The API has the last word (it also
 * refuses invisible characters and a name that exists).
 */
export function skillProblem(f: { name: string; description: string; body: string }): string {
  if (!validSkillName(f.name)) {
    return 'The name may contain lower case letters, digits and dashes, must start with a letter or a digit and has 1 to 40 characters.'
  }
  const description = f.description.trim()
  if (description === '') return 'The description must not be empty.'
  if ([...description].length > MAX_DESCRIPTION) return 'The description has at most 200 characters.'
  if (/\p{Cc}/u.test(description)) return 'The description is one line of plain text: no line breaks and no control characters.'
  const body = f.body.trim()
  if (body === '') return 'The body must not be empty.'
  if (byteLength(body) > MAX_BODY_BYTES) return 'The body has at most 8 KB.'
  return ''
}

export interface ScopeGroup {
  key: string
  title: string
  hostId: number | null
  skills: Skill[]
}

/** The skills that are not proposals, grouped by scope: Global first, then each host by name; each group by skill name. */
export function scopeGroups(skills: readonly Skill[]): ScopeGroup[] {
  const groups = new Map<string, ScopeGroup>()
  for (const s of skills) {
    if (s.state === 'proposed') continue
    const key = s.hostId === null ? 'global' : `host:${s.hostId}`
    let group = groups.get(key)
    if (!group) {
      group = { key, title: s.hostId === null ? 'Global' : (s.host ?? `Host ${s.hostId}`), hostId: s.hostId, skills: [] }
      groups.set(key, group)
    }
    group.skills.push(s)
  }
  const out = [...groups.values()]
  for (const g of out) g.skills.sort((a, b) => a.name.localeCompare(b.name))
  out.sort((a, b) => (a.hostId === null ? -1 : b.hostId === null ? 1 : a.title.localeCompare(b.title)))
  return out
}

/** A command as one line the way a shell would read it: words with a space, a quote or another special character are quoted. */
export function commandText(argv: readonly string[]): string {
  return argv.map((w) => (/^[A-Za-z0-9_@%+=:,./-]+$/.test(w) ? w : `'${w.replace(/'/g, `'\\''`)}'`)).join(' ')
}

export interface EvidenceLine {
  text: string
  ran: boolean
}

export interface ReviewItem {
  skill: Skill
  /** 'Global' or the host's name. */
  scope: string
  evidence: EvidenceLine[]
  /** How many of the cited commands the learning run did not run. */
  notRun: number
  /** The skill this proposal replaces, as it is now. */
  replaces: { description: string; body: string; changed: boolean } | null
}

/** The proposals, in the order the server sent them, with what the review list shows for each. */
export function reviewItems(skills: readonly Skill[]): ReviewItem[] {
  return skills
    .filter((s) => s.state === 'proposed')
    .map((s) => {
      const evidence = s.evidence.map((e) => ({ text: commandText(e.argv), ran: e.ran }))
      return {
        skill: s,
        scope: s.hostId === null ? 'Global' : (s.host ?? `Host ${s.hostId}`),
        evidence,
        notRun: evidence.filter((e) => !e.ran).length,
        replaces: s.replaces
          ? { description: s.replaces.description, body: s.replaces.body, changed: s.replaces.description !== s.description || s.replaces.body !== s.body }
          : null,
      }
    })
}

/** What the maintainer should know about the commands behind a proposal; '' when all of them ran. */
export function evidenceSummary(item: ReviewItem): string {
  const total = item.evidence.length
  if (total === 0) return 'No command is cited for this skill.'
  if (item.notRun === 0) return ''
  if (item.notRun === total) return 'None of the cited commands ran.'
  return `${item.notRun} of ${total} cited commands did not run.`
}

/** Who wrote a skill. The day is the ISO date of the confirmation, so that the text does not depend on the browser's locale. */
export function originText(s: Skill): string {
  if (s.origin === 'written') return 'written by you'
  return s.confirmedAt ? `proposed by an agent, confirmed on ${s.confirmedAt.slice(0, 10)}` : 'proposed by an agent'
}

/** Why a proposal cannot be confirmed now, or ''. The server checks again; this saves the click. */
export function confirmBlocked(all: readonly Skill[], p: Skill): string {
  if (p.replaces) return ''
  const inScope = all.filter((s) => s.state !== 'proposed' && s.hostId === p.hostId)
  if (inScope.some((s) => s.name === p.name)) return 'A skill with this name already exists. Reject this proposal, or rename or delete the other skill first.'
  if (inScope.length >= MAX_PER_SCOPE) return `This scope already has ${MAX_PER_SCOPE} skills. Delete one first.`
  return ''
}

const noun = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`

/** What deleting a host takes with it, from the skills of that host (proposals included); '' when nothing goes. */
export function hostDeleteWarning(skills: readonly Skill[]): string {
  const kept = skills.filter((s) => s.state !== 'proposed')
  const waiting = skills.length - kept.length
  const parts: string[] = []
  if (kept.length > 0) {
    const names = kept.slice(0, 5).map((s) => s.name).join(', ')
    parts.push(`${noun(kept.length, 'skill')} (${names}${kept.length > 5 ? ` and ${kept.length - 5} more` : ''})`)
  }
  if (waiting > 0) parts.push(`${noun(waiting, 'proposal')} waiting for review`)
  return parts.length === 0 ? '' : `Deleting this host also deletes what I know about it: ${parts.join(' and ')}.`
}

/** The second click of "Delete host". `null` means the skills are not known (the request failed). */
export function deleteHostLabel(skills: readonly Skill[] | null): string {
  if (skills === null) return 'Confirm: also removes its login and its skills'
  if (skills.length === 0) return 'Confirm: also removes its login'
  return `Confirm: also removes its login and ${noun(skills.length, 'skill')}`
}

/** The number of proposals after one poll: the count of an answer, or the last number when the poll failed. */
export function nextProposals(prev: number, result: PromiseSettledResult<readonly Skill[]>): number {
  return result.status === 'fulfilled' ? result.value.length : prev
}

export type SkillAction = 'created' | 'saved' | 'confirmed' | 'rejected' | 'deleted' | 'on' | 'off'

/** The sentence the live region says after an action. */
export function skillNotice(action: SkillAction, name: string): string {
  switch (action) {
    case 'created':
      return `Saved ${name}. I list it for every run it fits.`
    case 'saved':
      return `Saved ${name}.`
    case 'confirmed':
      return `Confirmed ${name}. I list it for every run it fits.`
    case 'rejected':
      return `Rejected the proposal ${name}.`
    case 'deleted':
      return `Deleted ${name}.`
    case 'on':
      return `${name} is on.`
    case 'off':
      return `${name} is off. I no longer list it.`
  }
}
