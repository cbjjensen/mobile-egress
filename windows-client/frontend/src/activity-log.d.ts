export type ActivitySeverity = 'info' | 'success' | 'warning' | 'error'

export type ActivityEvent = {
  id: string
  timestamp: string
  instanceId: string
  instanceName: string
  action: string
  severity: ActivitySeverity
  message: string
}

export function appendActivityEvent(events: ActivityEvent[], event: ActivityEvent): ActivityEvent[]
export function filterActivityEvents(events: ActivityEvent[], instanceId: string): ActivityEvent[]
export function formatActivityEvents(events: ActivityEvent[]): string
