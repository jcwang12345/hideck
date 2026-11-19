<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch, type Component } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { usePhoneStore } from '../stores/phone'
import { Expand, Fold } from '@element-plus/icons-vue'
import LoadingScreen from '../components/LoadingScreen.vue'
import ErrorBoundary from '../components/ErrorBoundary.vue'
import SwitchDark from '../components/SwitchDark.vue'
import { t, useLocale } from '../i18n'
import { nextLocale } from '../utils/locale'
import PhoneCallBar from '../components/PhoneCallBar.vue'
import SmsNotificationCenter from '../components/sms/SmsNotificationCenter.vue'
import { debugCollector } from '../debug/collector'
import {
  Mail24Regular,
  Settings24Regular,
  SignOut24Regular,
  Board24Regular,
  Phone24Regular,
  Globe24Regular,
  DocumentText24Regular,
  Chat24Regular,
  CalendarClock24Regular,
  Dialpad24Regular,
  GridDots24Regular
} from '@vicons/fluent'

defineProps({
  isDark: {
    type: Boolean,
    required: true
  }
})

const emit = defineEmits(['toggle-theme'])

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const phone = usePhoneStore()
const collapsed = ref(localStorage.getItem('sidebar_collapsed') === '1')
const isMobile = ref(false)
const viewportCompact = ref(false)
const viewportNarrow = ref(false)
const drawerOpen = ref(false)
const debugOpen = ref(false)
const DebugPanel = defineAsyncComponent(() => import('../components/DebugPanel.vue'))

const { locale, setLocale } = useLocale()
const localeHint = computed(() => (locale.value === 'en' ? t('locale.switchToZh') : t('locale.switchToEn')))
const localeMark = computed(() => (locale.value === 'en' ? '中' : 'EN'))
function toggleLocale() {
  setLocale(nextLocale(locale.value))
}
const menuItems = computed(() => [
  { index: '/', label: t('nav.dashboard'), icon: Board24Regular },
  { index: '/devices', label: t('nav.devices'), icon: Phone24Regular },
  { index: '/phone', label: t('nav.phone'), icon: Dialpad24Regular },
  { index: '/proxy', label: t('nav.proxy'), icon: Globe24Regular },
  { index: '/sms', label: t('nav.sms'), icon: Mail24Regular },
  { index: '/commands', label: t('nav.commands'), icon: Chat24Regular },
  { index: '/automatic-tasks', label: t('nav.tasks'), icon: CalendarClock24Regular },
  { index: '/logs', label: t('nav.logs'), icon: DocumentText24Regular },
  { index: '/settings', label: t('nav.settings'), icon: Settings24Regular }
])
type MobileMenuItem = {
  index: string
  label: string
  icon: Component
  prominent?: boolean
  drawer?: boolean
}

const mobileMenuItems = computed<MobileMenuItem[]>(() => [
  { index: '/', label: t('nav.dashboard'), icon: Board24Regular },
  { index: '/devices', label: locale.value === 'en' ? 'Devices' : '设备', icon: Phone24Regular },
  { index: '/phone', label: locale.value === 'en' ? 'Dial' : '拨号', icon: Dialpad24Regular, prominent: true },
  { index: '/sms', label: locale.value === 'en' ? 'SMS' : '短信', icon: Mail24Regular },
  { index: '__more__', label: locale.value === 'en' ? 'More' : '更多', icon: GridDots24Regular, drawer: true }
])
const effectiveCollapsed = computed(() => collapsed.value || viewportCompact.value)
const expandedSidebarWidth = computed(() => viewportNarrow.value ? '184px' : '208px')
const primaryMobilePaths = ['/', '/devices', '/phone', '/sms']

async function handleLogout() {
  const { ElMessageBox } = await import('element-plus')
  const confirmed = await ElMessageBox.confirm(t('nav.logoutConfirm'), t('nav.logoutTitle'), {
    confirmButtonText: t('nav.logoutAction'),
    cancelButtonText: t('common.cancel'),
    type: 'warning'
  })
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  auth.logout()
  router.push('/login')
}

function syncIsMobile() {
  if (typeof window === 'undefined') return
  isMobile.value = window.matchMedia('(max-width: 820px)').matches
  viewportCompact.value = !isMobile.value && window.matchMedia('(max-width: 1180px)').matches
  viewportNarrow.value = window.matchMedia('(max-width: 1480px)').matches
  if (!isMobile.value) {
    drawerOpen.value = false
  }
}

function handleNavToggle() {
  if (isMobile.value) {
    drawerOpen.value = true
    return
  }
  collapsed.value = !collapsed.value
  localStorage.setItem('sidebar_collapsed', collapsed.value ? '1' : '0')
}

function handleMobileNav(item: MobileMenuItem) {
  if (item.drawer) {
    drawerOpen.value = true
    return
  }
  void router.push(item.index)
}

function mobileItemActive(item: MobileMenuItem) {
  return item.drawer ? !primaryMobilePaths.includes(activePath.value) : activePath.value === item.index
}

function onKeydown(e: KeyboardEvent) {
  if (e.ctrlKey && e.shiftKey && String(e.key || '').toLowerCase() === 'd') {
    e.preventDefault()
    debugOpen.value = !debugOpen.value
    localStorage.setItem('debug_panel_open', debugOpen.value ? '1' : '0')
  }
}

onMounted(() => {
  syncIsMobile()
  window.addEventListener('resize', syncIsMobile, { passive: true })

  const saved = localStorage.getItem('debug_panel_open')
  debugOpen.value = saved === '1'

  window.addEventListener('keydown', onKeydown)
  void phone.initialize()
})

onUnmounted(() => {
  window.removeEventListener('resize', syncIsMobile)
  window.removeEventListener('keydown', onKeydown)
  phone.dispose()
})

watch(
  () => route.fullPath,
  () => {
    drawerOpen.value = false
  }
)

watch(
  () => debugOpen.value,
  (v) => {
    localStorage.setItem('debug_panel_open', v ? '1' : '0')
  }
)

watch(
  () => debugCollector.openPanelRequestAt.value,
  (ts) => {
    if (!ts) return
    debugOpen.value = true
  }
)

const activePath = computed(() => route.path)
const activeMenuItem = computed(() => menuItems.value.find((item) => item.index === route.path) || menuItems.value[0])
</script>

<template>
  <el-container v-if="auth.isAuthenticated && route.name !== 'Login'" class="h-full flow-shell">
    <el-aside
      v-if="!isMobile"
      :width="effectiveCollapsed ? '68px' : expandedSidebarWidth"
      class="h-full transition-[width] duration-200 relative sidebar-shell app-sidebar"
    >
      <div class="h-16 px-4 flex items-center sidebar-brand" :class="effectiveCollapsed ? 'justify-center px-0' : ''">
        <div class="sidebar-brand-icon">H</div>
        <div v-if="!effectiveCollapsed" class="ml-3 min-w-0">
          <div class="sidebar-brand-title">HiDeck</div>
          <div class="sidebar-brand-subtitle">MODEM CONTROL</div>
        </div>
      </div>

      <el-menu
        :collapse="effectiveCollapsed"
        :collapse-transition="false"
        :default-active="activePath"
        class="sidebar-menu !border-0 !border-r-0 !bg-transparent mt-2"
        router
      >
        <el-menu-item v-for="item in menuItems" :key="item.index" :index="item.index">
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title><span class="sidebar-menu-label">{{ item.label }}</span></template>
        </el-menu-item>
      </el-menu>

      <div v-if="effectiveCollapsed" class="sidebar-account-compact">
        <el-tooltip :content="localeHint" placement="right">
          <button type="button" :aria-label="localeHint" @click="toggleLocale">
            <span class="sidebar-locale-mark">{{ localeMark }}</span>
          </button>
        </el-tooltip>
        <el-tooltip :content="t('nav.logout')" placement="right">
          <button type="button" :aria-label="t('nav.logout')" @click="handleLogout">
            <el-icon><SignOut24Regular /></el-icon>
          </button>
        </el-tooltip>
      </div>
      <div v-else class="sidebar-account-expanded">
        <div class="sidebar-account flex items-center gap-3">
          <button type="button" class="sidebar-account-icon" :aria-label="localeHint" :title="localeHint" @click="toggleLocale">
            <span class="sidebar-locale-mark">{{ localeMark }}</span>
          </button>
          <div class="flex-1 min-w-0">
            <div class="text-sm font-semibold truncate text-[var(--ui-nav-text)]">{{ t('nav.admin') }}</div>
            <div class="text-xs truncate sidebar-account-role">{{ t('nav.administrator') }}</div>
          </div>
          <el-button text type="danger" :aria-label="t('nav.logout')" @click="handleLogout">
            <el-icon><SignOut24Regular /></el-icon>
          </el-button>
        </div>
      </div>
    </el-aside>

    <el-drawer v-model="drawerOpen" direction="ltr" size="256px" :with-header="false" class="mobile-drawer">
      <div class="h-full relative sidebar-shell app-sidebar">
        <div class="h-16 px-4 flex items-center">
          <div class="sidebar-brand-icon">H</div>
          <div class="ml-3 min-w-0">
            <div class="sidebar-brand-title">HiDeck</div>
            <div class="sidebar-brand-subtitle">MODEM CONTROL</div>
          </div>
        </div>

        <el-menu
          :collapse="false"
          :collapse-transition="false"
          :default-active="activePath"
          class="sidebar-menu !border-0 !border-r-0 !bg-transparent mt-2"
          router
        >
          <el-menu-item v-for="item in menuItems" :key="item.index" :index="item.index">
            <el-icon><component :is="item.icon" /></el-icon>
            <template #title><span class="sidebar-menu-label">{{ item.label }}</span></template>
          </el-menu-item>
        </el-menu>

        <div class="absolute bottom-3 w-full px-3">
          <div class="sidebar-account flex items-center gap-3">
            <button type="button" class="sidebar-account-icon" :aria-label="localeHint" :title="localeHint" @click="toggleLocale">
              <span class="sidebar-locale-mark">{{ localeMark }}</span>
            </button>
            <div class="flex-1 min-w-0">
              <div class="text-sm font-semibold truncate text-[var(--ui-nav-text)]">{{ t('nav.admin') }}</div>
              <div class="text-xs truncate sidebar-account-role">{{ t('nav.administrator') }}</div>
            </div>
            <el-button text type="danger" @click="handleLogout">
              <el-icon><SignOut24Regular /></el-icon>
            </el-button>
          </div>
        </div>
      </div>
    </el-drawer>

    <el-container class="h-full">
      <el-header class="app-topbar h-16 px-3 sm:px-5 flex items-center justify-between sticky top-0 z-10">
        <div class="topbar-side topbar-side-left">
          <el-button text :aria-label="isMobile ? t('nav.openNav') : collapsed ? t('nav.expandSidebar') : t('nav.collapseSidebar')" @click="handleNavToggle" class="nav-toggle !px-2">
            <el-icon>
              <Expand v-if="isMobile || collapsed" />
              <Fold v-else />
            </el-icon>
          </el-button>
          <div class="topbar-context">
            <span class="topbar-product">HIDECK</span>
            <strong>{{ activeMenuItem.label }}</strong>
          </div>
        </div>

        <div class="topbar-side topbar-side-right">
          <div class="hidden sm:flex service-state" :aria-label="t('nav.live')">
            <span class="service-state-dot" />
            <span>{{ t('nav.live') }}</span>
          </div>
          <SmsNotificationCenter />
          <SwitchDark :is-dark="isDark" @toggle="(e) => emit('toggle-theme', e)" />
        </div>
      </el-header>

      <PhoneCallBar />

      <el-main class="app-main px-4 pb-6 sm:px-7 sm:pb-8 overflow-auto">
        <div class="main-inner mx-auto w-full">
          <router-view v-slot="{ Component, route: r }">
            <ErrorBoundary v-if="Component" title="页面渲染失败">
              <component :is="Component" :key="r.fullPath" />
            </ErrorBoundary>
            <LoadingScreen v-else :title="t('common.loadingPage')" :subtitle="t('common.loadingPageHint')" />
          </router-view>
        </div>
      </el-main>
    </el-container>

    <nav v-if="isMobile" class="mobile-bottom-nav" :aria-label="t('nav.mobileNav')">
      <button
        v-for="item in mobileMenuItems"
        :key="item.index"
        type="button"
        :class="{
          'is-active': mobileItemActive(item),
          'is-prominent': item.prominent,
          'is-more': item.drawer
        }"
        :aria-label="item.label"
        :aria-current="!item.drawer && activePath === item.index ? 'page' : undefined"
        :aria-expanded="item.drawer ? drawerOpen : undefined"
        @click="handleMobileNav(item)"
      >
        <span class="mobile-nav-icon"><el-icon><component :is="item.icon" /></el-icon></span>
        <span class="mobile-nav-label">{{ item.label }}</span>
      </button>
    </nav>

    <DebugPanel v-model="debugOpen" />
  </el-container>
</template>

<style scoped>
.flow-shell {
  background: var(--ui-bg);
}

.flow-shell > :deep(.el-container) {
  min-width: 0;
}

.sidebar-shell {
  font-family: var(--ui-font-sans);
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
  --sidebar-menu-text: var(--ui-nav-text);
  --sidebar-menu-hover-bg: color-mix(in srgb, var(--ui-selected) 62%, transparent);
  --sidebar-menu-active-bg: var(--ui-selected);
  --sidebar-menu-active-color: var(--ui-nav-active);
}

:deep(.sidebar-menu) {
  margin-top: 0 !important;
  padding: 12px 8px;
  border-right: 0 !important;
  --el-menu-hover-bg-color: var(--sidebar-menu-hover-bg);
  --el-menu-active-color: var(--sidebar-menu-active-color);
  --el-menu-text-color: var(--sidebar-menu-text);
}

:deep(.sidebar-menu .el-menu-item) {
  position: relative;
  height: 40px;
  min-height: 40px;
  line-height: 40px;
  margin: 0 0 3px;
  padding-right: 12px !important;
  padding-left: 12px !important;
  border-radius: var(--ui-radius-nav);
  color: var(--sidebar-menu-text);
  font-size: 13px;
  font-weight: 500;
  letter-spacing: 0;
  transition: background-color 150ms ease, color 150ms ease;
}

:deep(.sidebar-menu .el-menu-item .el-icon) {
  margin-right: 11px !important;
  font-size: 18px;
}

:deep(.sidebar-menu .el-menu-item .el-icon svg) {
  width: 18px;
  height: 18px;
}

:deep(.sidebar-menu .el-menu-item:hover) {
  background: var(--sidebar-menu-hover-bg);
}

:deep(.sidebar-menu .el-menu-item.is-active) {
  background: var(--sidebar-menu-active-bg);
  color: var(--sidebar-menu-active-color);
}

:deep(.sidebar-menu .el-menu-item.is-active::before) {
  position: absolute;
  top: 8px;
  bottom: 8px;
  left: 0;
  width: 2px;
  border-radius: 0 2px 2px 0;
  background: var(--ui-accent);
  content: "";
}

:deep(.sidebar-menu .el-menu-item.is-active .el-icon),
:deep(.sidebar-menu .el-menu-item.is-active .sidebar-menu-label) {
  color: inherit;
}

:deep(.sidebar-menu .el-menu-item::after) {
  display: none !important;
}

:deep(.sidebar-menu.el-menu--collapse) {
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item) {
  width: 40px;
  height: 40px;
  min-height: 40px;
  margin: 0 auto 3px;
  padding: 0 !important;
  display: grid;
  place-items: center;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item .el-icon) {
  width: 18px;
  height: 18px;
  margin: 0 !important;
  display: grid;
  place-items: center;
  font-size: 18px;
  line-height: 1;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item .el-menu-tooltip__trigger) {
  position: static;
  inset: auto;
  width: 100%;
  height: 100%;
  padding: 0 !important;
  display: grid;
  place-items: center;
}

:deep(.sidebar-menu.el-menu--collapse > .el-menu-item [class^=el-icon]) {
  width: 18px !important;
}

:deep(.sidebar-menu.el-menu--collapse .el-tooltip),
:deep(.sidebar-menu.el-menu--collapse .el-tooltip__trigger) {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
}

:deep(.mobile-drawer) {
  border-radius: 0 var(--ui-radius-xl) var(--ui-radius-xl) 0;
}

:deep(.mobile-drawer .el-drawer__body) {
  padding: 0 !important;
}

.app-sidebar {
  border: 0;
  border-right: 1px solid var(--ui-border);
  border-radius: 0;
  background: var(--ui-nav-surface);
  color: var(--ui-nav-text);
  box-shadow: none;
}

.sidebar-brand {
  height: 64px !important;
  flex: 0 0 64px;
  border-bottom: 1px solid color-mix(in srgb, var(--ui-nav-text) 8%, transparent);
}

.sidebar-brand-title {
  color: var(--ui-nav-text);
  font-size: 17px;
  font-weight: 700;
  line-height: 1.1;
}

.sidebar-brand-subtitle {
  margin-top: 2px;
  color: var(--ui-nav-muted);
  font-family: var(--ui-font-mono);
  font-size: var(--ui-font-caption);
  font-weight: 500;
  letter-spacing: .04em;
}

.sidebar-brand-icon {
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  flex: 0 0 34px;
  border: 1px solid color-mix(in srgb, var(--ui-accent) 36%, var(--ui-border));
  border-radius: var(--ui-radius-md);
  background: var(--ui-accent);
  color: #fff;
  font-size: 16px;
  font-weight: 700;
}

.sidebar-menu-label {
  font-weight: 500;
}

.sidebar-account {
  min-height: 46px;
  padding: 6px;
  border: 1px solid color-mix(in srgb, var(--ui-nav-text) 10%, transparent);
  border-radius: var(--ui-radius-lg);
  background: color-mix(in srgb, var(--ui-nav-text) 3%, transparent);
}

.sidebar-account-compact {
  position: absolute;
  bottom: 12px;
  left: 0;
  width: 100%;
  display: grid;
  gap: 6px;
  place-items: center;
}

.sidebar-account-expanded {
  position: absolute;
  right: 8px;
  bottom: 10px;
  left: 8px;
}

.sidebar-account-compact button {
  width: 38px;
  height: 38px;
  display: grid;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--ui-nav-text) 10%, transparent);
  border-radius: var(--ui-radius-md);
  background: color-mix(in srgb, var(--ui-nav-text) 3%, transparent);
  color: var(--sidebar-menu-text);
  cursor: pointer;
}

.sidebar-account-icon {
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  flex: 0 0 32px;
  padding: 0;
  border: 0;
  border-radius: var(--ui-radius-md);
  background: color-mix(in srgb, var(--ui-accent) 10%, transparent);
  color: var(--ui-accent);
  cursor: pointer;
}

.sidebar-account-icon:hover,
.sidebar-account-icon:focus-visible {
  background: color-mix(in srgb, var(--ui-accent) 17%, transparent);
  color: var(--ui-nav-active);
}

.sidebar-locale-mark {
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
}

.sidebar-account-compact .sidebar-locale-mark {
  font-size: 13px;
}

.sidebar-account-role {
  color: var(--ui-nav-muted);
}

.app-topbar {
  width: 100%;
  height: var(--ui-shell-topbar-height) !important;
  margin: 0 !important;
  padding: 0 20px !important;
  flex: 0 0 var(--ui-shell-topbar-height);
  border-width: 0 0 1px;
  border-style: solid;
  border-color: var(--ui-border);
  border-radius: 0;
  background: color-mix(in srgb, var(--ui-surface) 96%, transparent);
  box-shadow: 0 1px 2px color-mix(in srgb, var(--ui-text) 4%, transparent);
  backdrop-filter: blur(12px);
}

.nav-toggle {
  width: 36px;
  min-height: 36px;
  margin-right: 4px;
  color: var(--ui-text-muted);
}

.topbar-context {
  min-width: 0;
  display: grid;
  gap: 1px;
}

.topbar-context strong {
  overflow: hidden;
  color: var(--ui-text);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.topbar-side {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.topbar-side-left {
  flex: 1 1 auto;
}

.topbar-side-right {
  flex: 0 0 auto;
  justify-content: flex-end;
}

.topbar-product {
  color: var(--ui-text-muted);
  font-family: var(--ui-font-mono);
  font-size: var(--ui-font-caption);
  letter-spacing: .08em;
  line-height: 1.2;
}

.topbar-product::after {
  content: ' / CONTROL PLANE';
}

.service-state {
  min-height: 30px;
  padding: 0 8px;
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--ui-success);
  font-size: 12px;
  font-weight: 500;
}

.service-state-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ui-success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--ui-success) 12%, transparent);
}

.app-main {
  padding: 24px 24px 32px !important;
  background: var(--ui-bg);
  overscroll-behavior: contain;
}

.main-inner {
  width: 100%;
  max-width: 1680px;
}

.mobile-bottom-nav {
  display: none;
}

@media (max-width: 1480px) and (min-width: 821px) {
  .app-main {
    padding: 20px 20px 28px !important;
  }
}

@media (max-width: 1180px) and (min-width: 821px) {
  .app-main {
    padding: 18px 16px 24px !important;
  }

  .app-topbar {
    padding-right: 14px !important;
    padding-left: 14px !important;
  }
}

@media (max-width: 820px) {
  .app-topbar {
    height: 54px !important;
    padding: 0 10px !important;
    flex-basis: 54px;
    padding-top: env(safe-area-inset-top) !important;
    box-sizing: content-box;
  }

  .topbar-product,
  .service-state {
    display: none !important;
  }

  .topbar-context strong {
    font-size: 16px;
  }

  .app-main {
    padding: 16px 12px calc(94px + env(safe-area-inset-bottom)) !important;
  }

  .mobile-bottom-nav {
    position: fixed;
    z-index: 40;
    right: 0;
    bottom: 0;
    left: 0;
    height: calc(68px + env(safe-area-inset-bottom));
    padding: 5px 6px env(safe-area-inset-bottom);
    display: grid;
    grid-template-columns: repeat(5, minmax(44px, 1fr));
    align-items: stretch;
    border-top: 1px solid var(--ui-border);
    background: color-mix(in srgb, var(--ui-surface) 96%, transparent);
    box-shadow: 0 -4px 14px color-mix(in srgb, var(--ui-text) 7%, transparent);
    backdrop-filter: blur(16px) saturate(1.1);
    box-sizing: border-box;
  }

  .mobile-bottom-nav button {
    position: relative;
    min-width: 0;
    min-height: 58px;
    padding: 4px 2px 3px;
    display: grid;
    grid-template-rows: 30px 16px;
    place-items: center;
    gap: 1px;
    border: 0;
    border-radius: var(--ui-radius-md);
    background: transparent;
    color: var(--ui-text-muted);
    cursor: pointer;
    -webkit-tap-highlight-color: transparent;
  }

  .mobile-bottom-nav button.is-active:not(.is-prominent) {
    background: color-mix(in srgb, var(--ui-primary) 9%, transparent);
    color: var(--ui-primary);
  }

  .mobile-nav-icon {
    width: 30px;
    height: 30px;
    display: grid;
    place-items: center;
    border-radius: var(--ui-radius-md);
  }

  .mobile-nav-icon .el-icon {
    font-size: 21px;
  }

  .mobile-nav-label {
    max-width: 100%;
    overflow: hidden;
    font-size: 12px;
    font-weight: 500;
    line-height: 16px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mobile-bottom-nav button.is-prominent {
    grid-template-rows: 36px 16px;
    color: var(--ui-primary);
  }

  .mobile-bottom-nav button.is-prominent .mobile-nav-icon {
    width: 50px;
    height: 50px;
    margin-top: -20px;
    border: 4px solid var(--ui-surface);
    border-radius: 50%;
    background: var(--ui-primary-solid);
    color: #fff;
    box-shadow: 0 4px 12px color-mix(in srgb, var(--ui-primary-solid) 20%, transparent);
  }

  .mobile-bottom-nav button.is-prominent .el-icon {
    font-size: 23px;
  }

  .mobile-bottom-nav button.is-prominent.is-active .mobile-nav-icon {
    background: var(--ui-primary);
  }

  :deep(.mobile-drawer) {
    padding-bottom: env(safe-area-inset-bottom);
  }
}
</style>
