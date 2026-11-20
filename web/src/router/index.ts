import { createRouter, createWebHashHistory, type RouteLocationNormalized } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { debugCollector } from '../debug/collector'
import { canAccess, homePathForUser } from '../utils/accessControl'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    roles?: string[]
    permissions?: string[]
  }
}

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: '/login',
      name: 'Login',
      component: () => import('../views/Login.vue')
    },
    {
      path: '/',
      name: 'Dashboard',
      component: () => import('../views/Dashboard.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/sms',
      name: 'SMS',
      component: () => import('../views/Sms.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/phone',
      name: 'Phone',
      component: () => import('../views/Phone.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/ut',
      redirect: '/'
    },
    {
      path: '/devices',
      name: 'Devices',
      component: () => import('../views/Devices.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/settings',
      name: 'Settings',
      component: () => import('../views/Settings.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/proxy',
      name: 'Proxy',
      component: () => import('../views/Proxy.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/logs',
      name: 'Logs',
      component: () => import('../views/Logs.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/commands',
      name: 'Commands',
      component: () => import('../views/Commands.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/automatic-tasks',
      name: 'AutomaticTasks',
      component: () => import('../views/AutomaticTasks.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/cards',
      name: 'Cards',
      component: () => import('../views/Cards.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/number-pools',
      name: 'NumberPools',
      component: () => import('../views/NumberPools.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/dispatch-policies',
      name: 'DispatchPolicies',
      component: () => import('../views/DispatchPolicies.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/users',
      name: 'Users',
      component: () => import('../views/Users.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/call-records',
      name: 'CallRecords',
      component: () => import('../views/CallRecords.vue'),
      meta: { requiresAuth: true }
    },
    {
      path: '/audit-log',
      name: 'AuditLog',
      component: () => import('../views/AuditLog.vue'),
      meta: { requiresAuth: true, roles: ['admin'] }
    },
    {
      path: '/403',
      name: 'Forbidden',
      component: () => import('../views/Forbidden.vue'),
      meta: { requiresAuth: true }
    }
  ]
})

router.beforeEach(async (to: RouteLocationNormalized) => {
  const authStore = useAuthStore()
  if (to.meta?.requiresAuth && !authStore.isAuthenticated) {
    return { name: 'Login', query: { redirect: to.fullPath } }
  }
  if (to.meta?.requiresAuth) {
    const ready = await authStore.initialize()
    if (!ready) return { name: 'Login', query: { redirect: to.fullPath } }
    if (to.path === '/' && authStore.role !== 'admin') return homePathForUser(authStore.user)
    if (!canAccess(authStore.user, { roles: to.meta.roles, permissions: to.meta.permissions })) {
      return { name: 'Forbidden', query: { from: to.fullPath } }
    }
  }
  if (to.name === 'Login' && authStore.isAuthenticated) {
    const ready = await authStore.initialize()
    if (ready) return homePathForUser(authStore.user)
  }
})

router.afterEach((to, from) => {
  debugCollector.recordRoute(to, from)
})

export default router
