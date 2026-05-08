import { Clock, Inbox, ListChecks, ListTree, Pause, Play, Settings } from 'lucide-react'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar'
import { Badge } from '@/components/ui/badge'

export type QueueId =
  | 'running'
  | 'backoff'
  | 'todo'
  | 'backlog'
  | 'recent_done'
  | 'canceled'

interface AppSidebarProps {
  active: QueueId
  onSelect: (id: QueueId) => void
  counts: Partial<Record<QueueId, number>>
  connected: boolean
  runtimeLabel: string
}

type NavItem = { id: QueueId; label: string; icon: React.ComponentType<{ className?: string }> }

const ACTIVE_GROUP: NavItem[] = [
  { id: 'running', label: '运行中', icon: Play },
  { id: 'backoff', label: '退避队列', icon: Pause },
]

const QUEUE_GROUP: NavItem[] = [
  { id: 'todo', label: '待办', icon: ListChecks },
  { id: 'backlog', label: 'Backlog', icon: Inbox },
]

const RECENT_GROUP: NavItem[] = [
  { id: 'recent_done', label: '最近完成', icon: ListTree },
  { id: 'canceled', label: '取消', icon: Clock },
]

function NavGroup({
  label,
  items,
  active,
  counts,
  onSelect,
}: {
  label: string
  items: NavItem[]
  active: QueueId
  counts: Partial<Record<QueueId, number>>
  onSelect: (id: QueueId) => void
}) {
  return (
    <SidebarGroup>
      <SidebarGroupLabel>{label}</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          {items.map((item) => {
            const Icon = item.icon
            const count = counts[item.id]
            return (
              <SidebarMenuItem key={item.id}>
                <SidebarMenuButton
                  isActive={active === item.id}
                  onClick={() => onSelect(item.id)}
                  className="h-9 rounded-xl px-2.5 font-medium transition-transform hover:translate-x-0.5 data-active:shadow-xs"
                >
                  <Icon className="h-4 w-4" />
                  <span>{item.label}</span>
                </SidebarMenuButton>
                {count !== undefined && count > 0 ? (
                  <SidebarMenuBadge className="right-2 rounded-full bg-background/70 px-1.5 text-primary">
                    {count}
                  </SidebarMenuBadge>
                ) : null}
              </SidebarMenuItem>
            )
          })}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

export function AppSidebar({ active, onSelect, counts, connected, runtimeLabel }: AppSidebarProps) {
  return (
    <Sidebar className="border-r border-sidebar-border/70 bg-sidebar/95 backdrop-blur">
      <SidebarHeader className="p-3">
        <div className="rounded-2xl border border-sidebar-border/70 bg-background/30 p-3 shadow-sm">
          <div className="flex items-center gap-3">
            <div className="flex h-11 w-11 items-center justify-center overflow-hidden rounded-2xl border border-primary/40 bg-primary/10 text-primary shadow-[0_0_24px_oklch(0.78_0.152_78_/_0.16)]">
              <img src="/contrabass.png" alt="" className="h-9 w-9 object-contain" />
            </div>
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="truncate text-base font-semibold leading-none tracking-tight">Ziikoo</span>
              <span className="mt-1 truncate font-mono text-xs text-muted-foreground">{runtimeLabel}</span>
            </div>
            <Badge
              variant={connected ? 'default' : 'destructive'}
              className="h-6 rounded-full px-2 text-[10px] font-semibold"
            >
              {connected ? '在线' : '离线'}
            </Badge>
          </div>
          <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-muted">
            <div
              className={`h-full rounded-full ${connected ? 'w-3/4 bg-gradient-to-r from-accent to-primary' : 'w-1/3 bg-destructive'}`}
              aria-hidden
            />
          </div>
        </div>
      </SidebarHeader>
      <SidebarContent className="gap-1 px-1">
        <NavGroup label="运行" items={ACTIVE_GROUP} active={active} counts={counts} onSelect={onSelect} />
        <NavGroup label="队列" items={QUEUE_GROUP} active={active} counts={counts} onSelect={onSelect} />
        <NavGroup label="归档" items={RECENT_GROUP} active={active} counts={counts} onSelect={onSelect} />
      </SidebarContent>
      <SidebarFooter className="p-3">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton tooltip="Settings (TODO)" className="h-9 rounded-xl text-muted-foreground hover:text-foreground">
              <Settings className="h-4 w-4" />
              <span>设置</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
