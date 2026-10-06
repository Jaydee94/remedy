import { useState } from 'react'
import type { Announcement } from './thread.ts'

/** What a page has told so far: the keys of the announcements it has seen, the text to show now, and whether the history was taken. */
export interface Announced {
  keys: ReadonlySet<string>
  text: string
  started: boolean
}

export const announcedAtStart: Announced = { keys: new Set(), text: '', started: false }

/**
 * The next state of a live region. `announcement` is the newest message, `history` all the messages the page has. The first call with
 * `ready` takes every message that is already on the page as seen, without a text (what was there at load is not news, and neither is
 * an older one that comes back once the newer one is dealt with). From then on a key that has not been seen becomes the text; a key
 * that was seen, and a null, change nothing. Returns `prev` itself when nothing changes.
 */
export function nextAnnounced(prev: Announced, announcement: Announcement | null, history: readonly Announcement[], ready: boolean): Announced {
  if (!ready) return prev
  if (!prev.started) return { keys: new Set(history.map((h) => h.key)), text: '', started: true }
  if (announcement === null || prev.keys.has(announcement.key)) return prev
  return { keys: new Set(prev.keys).add(announcement.key), text: announcement.text, started: true }
}

/**
 * The text for a live region of a page: it changes only when the announcement is new since the page was loaded. `ready` says that the
 * page has all its data (until then nothing is taken as history). The state is derived from the input during the render, which React
 * documents for this case; the second render finds nothing to change.
 */
export function useAnnouncement(announcement: Announcement | null, history: readonly Announcement[], ready: boolean): string {
  const [announced, setAnnounced] = useState(announcedAtStart)
  const next = nextAnnounced(announced, announcement, history, ready)
  if (next !== announced) setAnnounced(next)
  return next.text
}
