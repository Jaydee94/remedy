import { externalLinkClass, refLabel, safeUrl } from './incidents.ts'

/** The ref of an incident, linked to its pull request when GitHub gave a usable address. */
export default function RefLink({ refName, url }: { refName: string; url?: string }) {
  const href = safeUrl(url)
  const label = refLabel(refName)
  if (!href) return <span>{label}</span>
  return (
    <a href={href} target="_blank" rel="noreferrer" className={externalLinkClass}>
      {label}
    </a>
  )
}
