import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { chatbotService } from '@/services/api'

export interface AgentTransfer {
  id: string
  contact_id: string
  contact_name: string
  phone_number: string
  whatsapp_account: string
  status: 'active' | 'resumed' | 'expired'
  source: 'manual' | 'flow' | 'keyword'
  agent_id?: string
  agent_name?: string
  team_id?: string
  team_name?: string
  transferred_by?: string
  transferred_by_name?: string
  notes?: string
  transferred_at: string
  resumed_at?: string
  resumed_by?: string
  resumed_by_name?: string
  // SLA fields
  sla_response_deadline?: string
  sla_resolution_deadline?: string
  sla_breached: boolean
  sla_breached_at?: string
  escalation_level: number
  escalated_at?: string
  picked_up_at?: string
  expires_at?: string
}

// Helper to determine SLA status
export type SLAStatus = 'ok' | 'warning' | 'breached' | 'expired'

export function getSLAStatus(transfer: AgentTransfer): SLAStatus {
  if (transfer.status === 'expired') return 'expired'
  if (transfer.sla_breached) return 'breached'

  // Check deadline status (even if backend hasn't marked as breached yet)
  if (transfer.sla_response_deadline && !transfer.picked_up_at) {
    const deadline = new Date(transfer.sla_response_deadline)
    const now = new Date()
    const timeLeft = deadline.getTime() - now.getTime()

    // Deadline passed - treat as breached
    if (timeLeft <= 0) {
      return 'breached'
    }

    // Warning if escalated or less than 20% time remaining
    if (transfer.escalation_level >= 1) {
      return 'warning'
    }

    const totalTime = deadline.getTime() - new Date(transfer.transferred_at).getTime()
    if (timeLeft < totalTime * 0.2) {
      return 'warning'
    }
  }

  // Check escalation even if no deadline set
  if (transfer.escalation_level >= 1) return 'warning'

  return 'ok'
}

export const useTransfersStore = defineStore('transfers', () => {
  const transfers = ref<AgentTransfer[]>([])
  const generalQueueCount = ref(0)
  const teamQueueCounts = ref<Record<string, number>>({})
  const isLoading = ref(false)
  const lastSyncedAt = ref<number>(0) // Timestamp of last WebSocket sync

  // Total queue count (general + all teams)
  const queueCount = computed(() => {
    const teamTotal = Object.values(teamQueueCounts.value).reduce((sum, count) => sum + count, 0)
    return generalQueueCount.value + teamTotal
  })

  const activeTransfers = computed(() =>
    transfers.value.filter(t => t.status === 'active')
  )

  const myTransfers = computed(() => {
    const userId = localStorage.getItem('user_id')
    return transfers.value.filter(t =>
      t.status === 'active' && t.agent_id === userId
    )
  })

  const unassignedCount = computed(() =>
    transfers.value.filter(t => t.status === 'active' && !t.agent_id).length
  )

  // Get active transfer for a specific contact
  function getActiveTransferForContact(contactId: string): AgentTransfer | undefined {
    return transfers.value.find(t => t.contact_id === contactId && t.status === 'active')
  }

  async function fetchTransfers(params?: { status?: string }) {
    isLoading.value = true
    try {
      const response = await chatbotService.listTransfers(params)
      const data = response.data.data || response.data
      transfers.value = data.transfers || []
      generalQueueCount.value = data.general_queue_count ?? 0
      teamQueueCounts.value = data.team_queue_counts ?? {}
    } catch (error) {
      console.error('Failed to fetch transfers:', error)
    } finally {
      isLoading.value = false
    }
  }

  function addTransfer(transfer: AgentTransfer) {
    // Mark as synced via WebSocket
    lastSyncedAt.value = Date.now()

    // Add to beginning (newest first for display, but server returns FIFO)
    const exists = transfers.value.some(t => t.id === transfer.id)
    if (!exists) {
      transfers.value.unshift(transfer)
      if (!transfer.agent_id) {
        if (transfer.team_id) {
          teamQueueCounts.value[transfer.team_id] = (teamQueueCounts.value[transfer.team_id] || 0) + 1
        } else {
          generalQueueCount.value++
        }
      }
      console.log('Transfer added to store:', transfer.id, 'Total:', transfers.value.length, 'Queue count:', queueCount.value)
    } else {
      console.log('Transfer already exists:', transfer.id)
    }
  }

  function updateTransfer(id: string, updates: Partial<AgentTransfer>): boolean {
    // Mark as synced via WebSocket
    lastSyncedAt.value = Date.now()

    const index = transfers.value.findIndex(t => t.id === id)
    if (index !== -1) {
      const oldTransfer = transfers.value[index]
      const updatedTransfer = { ...oldTransfer, ...updates }

      // Use splice for proper Vue reactivity (array element replacement)
      transfers.value.splice(index, 1, updatedTransfer)

      // Update queue count if assignment changed
      if (updates.agent_id !== undefined) {
        if (!oldTransfer.agent_id && updates.agent_id) {
          // Was unassigned, now assigned - decrease queue count
          if (oldTransfer.team_id) {
            teamQueueCounts.value[oldTransfer.team_id] = Math.max(0, (teamQueueCounts.value[oldTransfer.team_id] || 0) - 1)
          } else {
            generalQueueCount.value = Math.max(0, generalQueueCount.value - 1)
          }
        } else if (oldTransfer.agent_id && !updates.agent_id) {
          // Was assigned, now unassigned - increase queue count
          const teamId = updates.team_id ?? oldTransfer.team_id
          if (teamId) {
            teamQueueCounts.value[teamId] = (teamQueueCounts.value[teamId] || 0) + 1
          } else {
            generalQueueCount.value++
          }
        }
      }

      // Update queue count if status changed to resumed
      if (updates.status === 'resumed' && oldTransfer.status === 'active' && !oldTransfer.agent_id) {
        if (oldTransfer.team_id) {
          teamQueueCounts.value[oldTransfer.team_id] = Math.max(0, (teamQueueCounts.value[oldTransfer.team_id] || 0) - 1)
        } else {
          generalQueueCount.value = Math.max(0, generalQueueCount.value - 1)
        }
      }

      return true
    }

    return false
  }

  function removeTransfer(id: string) {
    // Mark as synced via WebSocket
    lastSyncedAt.value = Date.now()

    const index = transfers.value.findIndex(t => t.id === id)
    if (index !== -1) {
      const transfer = transfers.value[index]
      if (transfer.status === 'active' && !transfer.agent_id) {
        if (transfer.team_id) {
          teamQueueCounts.value[transfer.team_id] = Math.max(0, (teamQueueCounts.value[transfer.team_id] || 0) - 1)
        } else {
          generalQueueCount.value = Math.max(0, generalQueueCount.value - 1)
        }
      }
      transfers.value.splice(index, 1)
    }
  }

  // Check if WebSocket sync is stale (no updates in given ms)
  function isSyncStale(thresholdMs: number = 60000): boolean {
    if (lastSyncedAt.value === 0) return true // Never synced
    return Date.now() - lastSyncedAt.value > thresholdMs
  }

  return {
    transfers,
    queueCount,
    generalQueueCount,
    teamQueueCounts,
    isLoading,
    lastSyncedAt,
    activeTransfers,
    myTransfers,
    unassignedCount,
    fetchTransfers,
    addTransfer,
    updateTransfer,
    removeTransfer,
    getActiveTransferForContact,
    isSyncStale
  }
})
