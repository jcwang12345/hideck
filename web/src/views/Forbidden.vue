<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ShieldError24Regular } from '@vicons/fluent'
import { useAuthStore } from '../stores/auth'
import { homePathForUser } from '../utils/accessControl'
import { t } from '../i18n'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const attemptedPath = computed(() => typeof route.query.from === 'string' ? route.query.from : '')
</script>

<template>
  <main class="forbidden-page">
    <section class="forbidden-card ui-card" role="alert">
      <span class="forbidden-icon" aria-hidden="true"><ShieldError24Regular /></span>
      <span class="forbidden-kicker">{{ t('resources.forbidden.kicker') }}</span>
      <h1>{{ t('resources.forbidden.title') }}</h1>
      <p>{{ t('resources.forbidden.message') }}</p>
      <code v-if="attemptedPath">{{ attemptedPath }}</code>
      <el-button type="primary" @click="router.replace(homePathForUser(auth.user))">{{ t('resources.forbidden.action') }}</el-button>
    </section>
  </main>
</template>

<style scoped>
.forbidden-page { min-height: min(640px, calc(100vh - 150px)); display: grid; place-items: center; }
.forbidden-card { width: min(520px, 100%); padding: 42px 34px; display: grid; justify-items: center; text-align: center; }
.forbidden-icon { width: 56px; height: 56px; display: grid; place-items: center; border-radius: var(--ui-radius-lg); background: var(--ui-danger-surface); color: var(--ui-danger); font-size: 28px; }
.forbidden-kicker { margin-top: 20px; color: var(--ui-primary); font: 600 var(--ui-font-caption)/1.2 var(--ui-font-mono); letter-spacing: .1em; }
h1 { margin: 8px 0 0; color: var(--ui-text); font-size: 24px; }
p { max-width: 420px; margin: 10px 0 0; color: var(--ui-text-muted); font-size: 13px; line-height: 1.65; }
code { max-width: 100%; margin: 16px 0 0; padding: 6px 10px; overflow-wrap: anywhere; border-radius: var(--ui-radius-sm); background: var(--ui-surface-muted); color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.el-button { margin-top: 22px; min-height: 44px; }
@media (max-width: 640px) { .forbidden-card { padding: 32px 22px; } }
</style>
