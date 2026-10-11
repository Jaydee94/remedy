/** Runs one request of the host page: one at a time, a 404 turns the whole page into "this host is gone", any other failure shows its message. */
export type Run = <T>(action: () => Promise<T>) => Promise<T | undefined>
