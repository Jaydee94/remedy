/** The only host the page shows for the sign-in link, and the only one it accepts. */
export const LOGIN_HOST = 'claude.com'

const urlPrefix = `https://${LOGIN_HOST}/cai/oauth/authorize`
const maxUrlBytes = 2048
const minCodeBytes = 8
const maxCodeBytes = 512

/** The query names the CLI was measured to send, as in the Go validator. A name that is not here fails closed. */
const queryNames = new Set(['code', 'client_id', 'response_type', 'redirect_uri', 'scope', 'code_challenge', 'code_challenge_method', 'state'])

/** Every character is 0x21 to 0x7E: no space, no control character, no DEL, nothing outside ASCII (so characters and bytes are the same). */
function printable(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c < 0x21 || c > 0x7e) return false
  }
  return true
}

/**
 * Mirrors `loginrelay.ValidateURL`, because the page does not trust the server: at most 2048 bytes of printable ASCII without a
 * backslash, exactly `https://claude.com/cai/oauth/authorize?` first (so no port, no userinfo, no look-alike, no other path), no
 * fragment, only the known query names, none twice, with `code_challenge` and `state`. Values are not judged. The shared cases of
 * `internal/loginrelay/testdata/cases.json` pin both sides to the same answers.
 */
export function validateUrl(raw: string): boolean {
  if (raw === '' || raw.length > maxUrlBytes || !printable(raw) || raw.includes('\\') || raw.includes('#')) return false
  if (!raw.startsWith(urlPrefix + '?')) return false
  const seen = new Set<string>()
  for (const part of raw.slice(urlPrefix.length + 1).split('&')) {
    const eq = part.indexOf('=')
    const name = eq < 0 ? part : part.slice(0, eq)
    if (!queryNames.has(name) || seen.has(name)) return false
    seen.add(name)
  }
  return seen.has('code_challenge') && seen.has('state')
}

/** The white space the server trims (`strings.Trim(raw, " \t\r\n\v\f")`), not everything `String.prototype.trim` treats as white space. */
const trimmed = (s: string) => s.replace(/^[ \t\r\n\v\f]+|[ \t\r\n\v\f]+$/g, '')

/**
 * Mirrors `loginrelay.ValidateCode`: trimmed of surrounding white space, 8 to 512 characters, each 0x21 to 0x7E, so it is one line. The
 * result carries the trimmed code and nothing else; the caller sends it and does not keep it.
 */
export function validateCode(raw: string): { ok: true; code: string } | { ok: false } {
  const code = trimmed(raw)
  if (code.length < minCodeBytes || code.length > maxCodeBytes || !printable(code)) return { ok: false }
  return { ok: true, code }
}

const owns = (table: Record<string, string>, word: string | undefined): word is string => word !== undefined && Object.hasOwn(table, word)

/** The sentence the page shows when a sign-in failed without any other word of its own. */
export const badAddressSentence = 'I got a sign-in address I do not recognise, so I did not show it'

const reasons: Record<string, string> = {
  no_url: 'The runner did not get a sign-in address in time, so nothing was started.',
  bad_url: badAddressSentence + '.',
  invalid_code: 'The code was not accepted three times, so I stopped. You can start a new sign-in and copy a fresh code.',
  process_exited: 'The runner ended the sign-in before it was done. You can log in from a terminal instead.',
  not_confirmed: 'The sign-in ended well, but the runner still does not say it is logged in. You can log in from a terminal instead.',
  timeout: 'The runner did not confirm the login in time, so I stopped.',
  runner_lost: 'I lost my runner during the sign-in, so I stopped.',
  runner_restarted: 'My runner restarted during the sign-in, so I stopped.',
  expired: 'The sign-in ran out of time after ten minutes, so I closed it.',
  cancelled: 'The sign-in was cancelled.',
  run_active: 'A run was running, so the runner did not start the sign-in. Try again when the run has ended.',
}

/** The reason as a word the page knows: itself, or `other` for any word it does not know. Use it where a word would otherwise be copied. */
export function knownReason(reason: string | undefined): string {
  return owns(reasons, reason) ? reason : reason === undefined || reason === '' ? '' : 'other'
}

/** One template sentence per failure reason; a word it does not know (or none) gets a generic one. The word itself is never shown. */
export function reasonSentence(reason: string | undefined): string {
  return owns(reasons, reason) ? reasons[reason] : 'The sign-in did not work.'
}

const refusals: Record<string, string> = {
  disabled: 'Logging in from this page is turned off.',
  runner_offline: 'My runner is not connected, so it cannot start a sign-in.',
  run_active: 'A run is running. I can start the sign-in when it has ended.',
  already_active: 'A sign-in is already open, maybe in another tab or window.',
  api_key_mode: 'My runner uses an API key, so there is no login to start.',
  replace_required: 'The runner is logged in already. Choose Log in again if you want to replace its login.',
}

/** One template sentence per refusal of a start; a word it does not know (or none) gets a generic one. */
export function refusalSentence(word: string | undefined): string {
  return owns(refusals, word) ? refusals[word] : 'I could not start the sign-in.'
}

/** What to say when a request of the open sign-in is answered with 409 (it moved on) or 429 (too many tries); null for any other status. */
export function conflictSentence(status: number): string | null {
  if (status === 409) return 'This sign-in moved on, maybe in another tab or window. I show where it stands now.'
  if (status === 429) return 'Too many tries. Please wait a moment and try again.'
  return null
}
