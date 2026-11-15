import { History, House, Lamp, Settings, Users, type LucideIcon } from 'lucide-react'
import type { TabId } from './navstack'

export interface TabDef {
  id: TabId
  root: string
  icon: LucideIcon
  label: string
}

/** The tabs of DESIGN.md §2.1. Люди is a placeholder until phase 3 (T3.4). */
export const TABS: TabDef[] = [
  { id: 'home', root: '/admin', icon: House, label: 'tab.home' },
  { id: 'devices', root: '/admin/devices', icon: Lamp, label: 'tab.devices' },
  { id: 'people', root: '/admin/people', icon: Users, label: 'tab.people' },
  { id: 'activity', root: '/admin/activity', icon: History, label: 'tab.activity' },
  { id: 'settings', root: '/admin/settings', icon: Settings, label: 'tab.settings' },
]
