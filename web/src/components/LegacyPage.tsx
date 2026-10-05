import { Outlet } from 'react-router'

/** The container of a page that is not rebuilt as a conversation yet. The part that rebuilds a page moves its route out of this wrapper. */
export default function LegacyPage() {
  return (
    <div className="mx-auto w-full max-w-5xl p-4 md:p-8">
      <Outlet />
    </div>
  )
}
