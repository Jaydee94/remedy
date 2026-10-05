/** Shown while the server cannot be reached. */
export default function OfflineBanner() {
  return (
    <div
      role="status"
      className="flex shrink-0 items-center justify-center gap-2.5 bg-soft-diagnosing px-4 py-2 text-center text-[13px] font-semibold text-primary"
    >
      <span aria-hidden className="size-1.75 shrink-0 rounded-full bg-primary" />
      Reconnecting… live updates are paused. What you see may be a minute old.
    </div>
  )
}
