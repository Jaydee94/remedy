interface Props {
  size?: number
  /** The colour of the highlight cut into the drop: the surface the mark sits on. */
  cutout?: string
  /** The colour of the drop. */
  fill?: string
  className?: string
}

/** The drop of Remedy. */
export default function RemedyMark({ size = 24, cutout = 'var(--background)', fill = 'var(--primary)', className }: Props) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} className={className} aria-hidden>
      <path d="M12 2.6c-.3.4-7 8.3-7 12.9a7 7 0 0 0 14 0c0-4.6-6.7-12.5-7-12.9z" fill={fill} />
      <path d="M8.7 15.4a3.3 3.3 0 0 0 3.3 3.3" stroke={cutout} strokeWidth="1.5" fill="none" strokeLinecap="round" />
    </svg>
  )
}
