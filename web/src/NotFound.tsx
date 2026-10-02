import { Link } from 'react-router'

export default function NotFound() {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <Link to="/incidents" className="text-sm text-muted-foreground hover:text-foreground">
        Back to incidents
      </Link>
    </div>
  )
}
