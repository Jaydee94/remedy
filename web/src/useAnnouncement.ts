import { useState } from 'react'
import type { Announcement } from './thread.ts'

/** What a page has told so far: the keys of the messages it has seen, the announcement to show now, and whether the history was taken. */
export interface Announced {
  keys: ReadonlySet<string>
  announcement: Announcement | null
  started: boolean
}

export const announcedAtStart: Announced = { keys: new Set(), announcement: null, started: false }

/**
 * The next state of a live region. `history` is every message the page has, oldest first. The first call with `ready` takes every
 * message that is already on the page as seen, without an announcement (what was there at load is not news, and neither is an older
 * one that comes back once the newer one is dealt with). From then on the messages that have not been seen are news: the newest of
 * them becomes the announcement, and all current messages count as seen (two that arrive in one step announce the newest, and the other
 * is never announced later at an odd moment). Returns `prev` itself when nothing is new.
 */
export function nextAnnounced(prev: Announced, history: readonly Announcement[], ready: boolean): Announced {
  if (!ready) return prev
  if (!prev.started) return { keys: new Set(history.map((h) => h.key)), announcement: null, started: true }
  const unseen = history.filter((h) => !prev.keys.has(h.key))
  if (unseen.length === 0) return prev
  return { keys: new Set([...prev.keys, ...history.map((h) => h.key)]), announcement: unseen[unseen.length - 1], started: true }
}

/**
 * The announcement for a live region of a page: it changes only when a message is new since the page was loaded. The page renders it
 * under a `key`, so a new message with the same words as the one before still replaces the node and is announced. `ready` says that the
 * page has all its data (until then nothing is taken as history). The state is derived from the input during the render, which React
 * documents for this case; the second render finds nothing to change.
 */
export function useAnnouncement(history: readonly Announcement[], ready: boolean): Announcement | null {
  const [announced, setAnnounced] = useState(announcedAtStart)
  const next = nextAnnounced(announced, history, ready)
  if (next !== announced) setAnnounced(next)
  return next.announcement
}
