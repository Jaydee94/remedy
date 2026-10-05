import { Link } from 'react-router'
import RemedyAvatar from '@/components/RemedyAvatar'
import { buttonVariants } from '@/components/ui/button'

interface Props {
  /** The sentence of Remedy, built by digestText. */
  text: string
  /** How many questions wait for a decision. */
  pending: number
  /** The incident diagnosed last, if there is one. */
  diagnosedId?: number
  /** The time shown in the line above the sentence. */
  time: string
}

/** Remedy's morning words: a sentence, and what to do about it. */
export default function Digest({ text, pending, diagnosedId, time }: Props) {
  return (
    <div className="flex gap-3.5">
      <RemedyAvatar />
      <div className="flex min-w-0 flex-col gap-3">
        <span className="text-xs text-subtle">Remedy · {time}</span>
        <p className="font-serif text-[clamp(19px,2.2vw,24px)] leading-[1.45] text-pretty break-words">{text}</p>
        {(pending > 0 || diagnosedId !== undefined) && (
          <div className="flex flex-wrap gap-2">
            {pending > 0 && (
              <Link to="/approvals" className={buttonVariants({ variant: 'default' })}>
                {pending === 1 ? 'Answer 1 question' : `Answer ${pending} questions`}
              </Link>
            )}
            {diagnosedId !== undefined && (
              <Link to={`/incidents/${diagnosedId}`} className={buttonVariants({ variant: 'outline' })}>
                Read the diagnosis
              </Link>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
