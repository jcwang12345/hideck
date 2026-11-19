<script setup lang="ts">
import LoadingScreen from '../components/LoadingScreen.vue'
import SwitchDark from '../components/SwitchDark.vue'
import SwitchLocale from '../components/SwitchLocale.vue'

defineProps({
  isDark: {
    type: Boolean,
    required: true
  }
})

const emit = defineEmits(['toggle-theme'])
</script>

<template>
  <div class="unauthenticated-shell h-screen flex items-center justify-center transition-colors duration-300">
    <div class="shell-controls">
      <SwitchLocale />
      <SwitchDark :is-dark="isDark" @toggle="(e) => emit('toggle-theme', e)" />
    </div>
    <router-view v-slot="{ Component }">
      <Suspense>
        <template #default>
          <component :is="Component" />
        </template>
        <template #fallback>
          <LoadingScreen />
        </template>
      </Suspense>
    </router-view>
  </div>
</template>

<style scoped>
.unauthenticated-shell {
  position: relative;
  width: 100%;
  min-height: 100dvh;
  padding: 20px;
  overflow-x: hidden;
  overflow-y: auto;
  background: var(--ui-bg);
}

.shell-controls {
  position: fixed;
  z-index: 50;
  top: calc(12px + env(safe-area-inset-top));
  right: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
}

@media (max-width: 640px) {
  .unauthenticated-shell {
    height: auto !important;
    min-height: 100dvh;
    padding: calc(64px + env(safe-area-inset-top)) 12px calc(20px + env(safe-area-inset-bottom));
    align-items: flex-start;
  }
}
</style>
