import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '@/services/api'

export interface UserSettings {
  email_notifications?: boolean
  new_message_alerts?: boolean
  campaign_updates?: boolean
}

export interface User {
  id: string
  email: string
  full_name: string
  role: string
  organization_id: string
  organization_name?: string
  settings?: UserSettings
  is_available?: boolean
}

export interface AuthState {
  user: User | null
  token: string | null
  refreshToken: string | null
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const token = ref<string | null>(null)
  const refreshToken = ref<string | null>(null)

  const isAuthenticated = computed(() => !!token.value && !!user.value)
  const userRole = computed(() => user.value?.role || 'agent')
  const organizationId = computed(() => user.value?.organization_id || '')
  const userSettings = computed(() => user.value?.settings || {})
  const isAvailable = computed(() => user.value?.is_available ?? true)

  function setAuth(authData: { user: User; access_token: string; refresh_token: string }) {
    user.value = authData.user
    token.value = authData.access_token
    refreshToken.value = authData.refresh_token

    // Store in localStorage
    localStorage.setItem('auth_token', authData.access_token)
    localStorage.setItem('refresh_token', authData.refresh_token)
    localStorage.setItem('user', JSON.stringify(authData.user))
  }

  function clearAuth() {
    user.value = null
    token.value = null
    refreshToken.value = null

    localStorage.removeItem('auth_token')
    localStorage.removeItem('refresh_token')
    localStorage.removeItem('user')
  }

  function restoreSession(): boolean {
    const storedToken = localStorage.getItem('auth_token')
    const storedRefreshToken = localStorage.getItem('refresh_token')
    const storedUser = localStorage.getItem('user')

    if (storedToken && storedUser) {
      try {
        token.value = storedToken
        refreshToken.value = storedRefreshToken
        user.value = JSON.parse(storedUser)
        return true
      } catch {
        clearAuth()
        return false
      }
    }
    return false
  }

  async function login(email: string, password: string): Promise<void> {
    const response = await api.post('/auth/login', { email, password })
    // fastglue wraps response in { status: "success", data: {...} }
    setAuth(response.data.data)
  }

  async function register(data: {
    email: string
    password: string
    full_name: string
    organization_name: string
  }): Promise<void> {
    const response = await api.post('/auth/register', data)
    // fastglue wraps response in { status: "success", data: {...} }
    setAuth(response.data.data)
  }

  async function logout(): Promise<void> {
    try {
      await api.post('/auth/logout')
    } catch {
      // Ignore logout errors
    } finally {
      clearAuth()
    }
  }

  async function refreshAccessToken(): Promise<boolean> {
    if (!refreshToken.value) return false

    try {
      const response = await api.post('/auth/refresh', {
        refresh_token: refreshToken.value
      })
      // fastglue wraps response in { status: "success", data: {...} }
      const data = response.data.data
      token.value = data.access_token
      localStorage.setItem('auth_token', data.access_token)
      return true
    } catch {
      clearAuth()
      return false
    }
  }

  function setAvailability(available: boolean) {
    if (user.value) {
      user.value = { ...user.value, is_available: available }
      localStorage.setItem('user', JSON.stringify(user.value))
    }
  }

  return {
    user,
    token,
    refreshToken,
    isAuthenticated,
    userRole,
    organizationId,
    userSettings,
    isAvailable,
    setAuth,
    clearAuth,
    restoreSession,
    login,
    register,
    logout,
    refreshAccessToken,
    setAvailability
  }
})
