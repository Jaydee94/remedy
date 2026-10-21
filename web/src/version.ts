import type { RunnerStatus } from './api.ts'

/** The build of a process: what `GET /api/version` answers, and the runner's own part of `GET /api/runner`. Either may be missing. */
export interface Build {
  version?: string
  commit?: string
}

/** A line of muted text, with the commit as its tooltip. */
export interface VersionLine {
  text: string
  title: string | undefined
}

/** A build without a release number of its own: two of them are the same only when they are the same commit. */
const unnumbered = new Set(['dev', 'edge'])

/** The version when it is known. The control plane says "unknown" for a value it refused: that is not a version. */
function known(b: Build | null | undefined): string | null {
  const v = b?.version?.trim()
  return v && v !== 'unknown' ? v : null
}

function commitOf(b: Build | null | undefined): string | null {
  const c = b?.commit?.trim()
  return c && c !== 'unknown' ? c : null
}

/** How a build is named in a sentence: a release by its number, a dev or edge build with its commit too. Null while it is not known. */
export function buildLabel(b: Build | null | undefined): string | null {
  const v = known(b)
  if (!v) return null
  const c = commitOf(b)
  return unnumbered.has(v) && c ? `${v} (${c})` : v
}

function line(prefix: string, b: Build | null | undefined): VersionLine | null {
  const v = known(b)
  if (!v) return null
  const c = commitOf(b)
  return { text: `${prefix} ${v}`, title: c ? `Commit ${c}` : undefined }
}

/** The line at the bottom of the sidebar. Null while the version is not known (an older control plane, or a request that failed). */
export function sidebarVersion(b: Build | null | undefined): VersionLine | null {
  return line('Remedy', b)
}

/** The runner's version in the Setup page. A runner that is not connected has none: what it said is a fact about the past. */
export function runnerVersionLine(r: RunnerStatus | null | undefined): VersionLine | null {
  return r && r.connected ? line('Runner', r) : null
}

/**
 * What to say when the runner and the control plane run different builds. Silent while either side is not known, and for two
 * dev or edge builds when one of them does not know its commit: nothing is guessed.
 */
export function versionNotice(control: Build | null | undefined, r: RunnerStatus | null | undefined): string | null {
  if (!r || !r.connected) return null
  const cv = known(control)
  const rv = known(r)
  if (!cv || !rv) return null
  if (cv === rv) {
    const cc = commitOf(control)
    const rc = commitOf(r)
    if (!unnumbered.has(cv) || !cc || !rc || cc === rc) return null
  }
  return `The runner runs ${buildLabel(r)}, the control plane ${buildLabel(control)}. Upgrade both together.`
}
