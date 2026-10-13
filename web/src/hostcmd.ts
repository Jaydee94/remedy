const PLAIN = /^[A-Za-z0-9@%+=:,./_-]+$/

/** A word as a person would type it: quoted with single quotes only when it needs them. Same rule as hostexec.Display in Go. */
export function quoteWord(word: string): string {
  return PLAIN.test(word) ? word : `'${word.replaceAll("'", `'\\''`)}'`
}

/** The command of a host call as one line of text. It is text, never markup. */
export function commandText(words: readonly string[]): string {
  return words.map(quoteWord).join(' ')
}

/** The argv of a host call's arguments: a non-empty list of strings, or undefined for anything else. */
export function argvOf(args: unknown): string[] | undefined {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) return undefined
  const argv = (args as Record<string, unknown>).argv
  if (!Array.isArray(argv) || argv.length === 0 || !argv.every((w) => typeof w === 'string')) return undefined
  return argv as string[]
}
