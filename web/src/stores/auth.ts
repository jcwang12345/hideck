import { defineStore } from 'pinia'
import axios, { type AxiosInstance } from 'axios'
import { debugCollector } from '../debug/collector'
import type { PasswordCredentialStatus } from '../types/credentials'
import type { AuthSession, AuthUser } from '../types/admin-resources'

export const api: AxiosInstance = axios.create({
  baseURL: '/api'
})

try {
  const token = localStorage.getItem('token') || ''
  if (token) {
    api.defaults.headers.common.Authorization = `Bearer ${token}`
  }
} catch {
  // localStorage may be unavailable in some sandboxed contexts.
}

type AuthState = {
  token: string
  user: AuthUser | null
  session: AuthSession | null
  initialized: boolean
  initializing: boolean
  initializationError: string
}

type LoginResult =
  | { ok: true; credential: PasswordCredentialStatus }
  | { ok: false }

type LoginResponse = {
  token?: string
  credential?: PasswordCredentialStatus
  user?: unknown
  permissions?: unknown
  session?: unknown
}

type MeResponse = {
  user?: unknown
  permissions?: unknown
  session?: unknown
}

function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return [...new Set(value.map(item => String(item || '').trim()).filter(Boolean))]
}

function normalizeUser(value: unknown, permissions?: unknown): AuthUser | null {
  if (!value || typeof value !== 'object') return null
  const raw = value as Record<string, unknown>
  const id = String(raw.id || '').trim()
  const username = String(raw.username || '').trim()
  const role = String(raw.role || '').trim().toLowerCase()
  if (!id || !username || !role) return null
  return {
    id,
    username,
    role,
    display_name: String(raw.display_name || '').trim() || undefined,
    disabled: raw.disabled === true,
    created_at: String(raw.created_at || '').trim() || undefined,
    updated_at: String(raw.updated_at || '').trim() || undefined,
    permissions: stringList(raw.permissions ?? permissions)
  }
}

function normalizeSession(value: unknown): AuthSession | null {
  if (!value || typeof value !== 'object') return null
  const raw = value as Record<string, unknown>
  const id = String(raw.id || '').trim()
  if (!id) return null
  return {
    id,
    expires_at: String(raw.expires_at || '').trim() || undefined
  }
}

function isCredentialStatus(value: unknown): value is PasswordCredentialStatus {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<PasswordCredentialStatus>
  return typeof candidate.change_required === 'boolean' &&
    (candidate.management === 'config_file' ||
      candidate.management === 'environment' ||
      candidate.management === 'database')
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    token: localStorage.getItem('token') || '',
    user: null,
    session: null,
    initialized: false,
    initializing: false,
    initializationError: ''
  }),
  getters: {
    isAuthenticated: (state: AuthState) => !!state.token,
    role: (state: AuthState) => state.user?.role || '',
    isAdmin: (state: AuthState) => state.user?.role === 'admin',
    displayName: (state: AuthState) => state.user?.display_name || state.user?.username || '',
    permissionSet: (state: AuthState) => new Set(state.user?.permissions || []),
    hasPermission(): (permission: string) => boolean {
      return (permission: string) => this.isAdmin || this.permissionSet.has(permission)
    }
  },
  actions: {
    applyToken(token: string) {
      this.token = token
      localStorage.setItem('token', token)
      api.defaults.headers.common.Authorization = `Bearer ${token}`
    },
    applyIdentity(payload: MeResponse) {
      const user = normalizeUser(payload.user, payload.permissions)
      if (!user) throw new Error('当前用户响应缺少有效身份')
      if (user.disabled) throw new Error('当前账号已被禁用')
      this.user = user
      this.session = normalizeSession(payload.session)
      this.initialized = true
      this.initializationError = ''
    },
    async initialize(force = false): Promise<boolean> {
      if (!this.token) {
        this.clearSession()
        this.initialized = true
        return false
      }
      if (!force && this.initialized && this.user) return true
      if (this.initializing) {
        while (this.initializing) await new Promise(resolve => setTimeout(resolve, 10))
        return !!this.user
      }
      this.initializing = true
      this.initializationError = ''
      try {
        const response = await api.get<MeResponse>('/auth/me')
        this.applyIdentity(response.data || {})
        return true
      } catch (error) {
        this.initializationError = error instanceof Error ? error.message : '读取当前用户失败'
        this.clearSession()
        return false
      } finally {
        this.initializing = false
      }
    },
    async login(username: string, password: string): Promise<LoginResult> {
      try {
        const res = await api.post<LoginResponse>('/auth/login', { username, password })
        const token = String(res.data?.token || '')
        if (!token || !isCredentialStatus(res.data?.credential)) {
          throw new Error('登录响应缺少会话或凭证管理状态')
        }
        this.applyToken(token)
        const user = normalizeUser(res.data?.user, res.data?.permissions)
        if (user) {
          if (user.disabled) throw new Error('当前账号已被禁用')
          this.user = user
          this.session = normalizeSession(res.data?.session)
          this.initialized = true
          this.initializationError = ''
        } else if (!await this.initialize(true)) {
          throw new Error('登录成功，但无法读取当前用户')
        }
        return { ok: true, credential: res.data.credential }
      } catch (e) {
        console.error(e)
        this.clearSession()
        return { ok: false }
      }
    },
    clearSession() {
      this.token = ''
      this.user = null
      this.session = null
      this.initialized = false
      this.initializationError = ''
      localStorage.removeItem('token')
      delete api.defaults.headers.common.Authorization
    },
    async logout() {
      const request = this.token
        ? api.post('/auth/logout').catch(() => undefined)
        : Promise.resolve(undefined)
      this.clearSession()
      await request
    }
  }
})

api.interceptors.response.use(
  (response) => response,
  (error) => {
    debugCollector.recordApiError(error)
    if (error?.response?.status === 401) {
      if (isPasswordValidationError(error)) {
        return Promise.reject(error)
      }
      try {
        const current = String(window.location.hash || '').replace(/^#/, '') || '/'
        if (!current.startsWith('/login')) {
          sessionStorage.setItem('post_login_redirect', current)
          debugCollector.recordAuthEvent({ ts: Date.now(), kind: '401_redirect', redirectTo: current })
          window.location.hash = `#/login?redirect=${encodeURIComponent(current)}`
          const auth = useAuthStore()
          auth.clearSession()
          return Promise.reject(error)
        }
      } catch {
        // Accessing sessionStorage/window hash can fail in restricted contexts.
      }
      const auth = useAuthStore()
      auth.clearSession()
      window.location.hash = '#/login'
    }
    return Promise.reject(error)
  }
)

function isPasswordValidationError(error: unknown): boolean {
  if (!axios.isAxiosError(error)) return false
  const data = error.response?.data as { code?: unknown } | undefined
  return error.config?.url === '/settings/password' && data?.code === 'invalid_password'
}
