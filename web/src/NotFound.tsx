import { Link } from 'react-router'

export default function NotFound() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">I can't find that page.</h1>
      <Link
        to="/incidents"
        className="w-fit rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        Back to Conversations
      </Link>
    </div>
  )
}
