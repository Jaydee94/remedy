import { Hand, MessagesSquare, Newspaper, Server, SlidersHorizontal, SquarePen } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { Section } from '@/shell.ts'

export const navIcons: Record<Section, LucideIcon> = {
  today: Newspaper,
  conversations: MessagesSquare,
  needs: Hand,
  ask: SquarePen,
  hosts: Server,
  setup: SlidersHorizontal,
}
