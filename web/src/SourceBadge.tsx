import type { IncidentSource } from './api.ts'
import { sourceLabel } from './incidents.ts'
import { Badge } from '@/components/ui/badge'

/** Where an incident comes from. */
export default function SourceBadge({ source }: { source: IncidentSource }) {
  return <Badge variant="secondary">{sourceLabel(source)}</Badge>
}
