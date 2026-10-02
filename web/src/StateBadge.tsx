import type { IncidentState } from './api.ts'
import { incidentStateColor } from './incidents.ts'
import { Badge } from '@/components/ui/badge'

export default function StateBadge({ state }: { state: IncidentState }) {
  return (
    <Badge variant="outline" className="gap-2">
      <span className={`h-2 w-2 rounded-full ${incidentStateColor[state]}`} />
      {state}
    </Badge>
  )
}
