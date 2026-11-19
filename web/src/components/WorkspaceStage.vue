<script setup lang="ts">
withDefaults(defineProps<{
  kicker: string
  title: string
  subtitle?: string
  status?: string
  tone?: 'success' | 'warning' | 'danger' | 'neutral'
  compact?: boolean
}>(), {
  subtitle: '',
  status: '',
  tone: 'neutral',
  compact: false
})
</script>

<template>
  <section class="workspace-stage" :class="{ 'is-compact': compact }">
    <div class="workspace-stage-main">
      <header class="workspace-stage-header">
        <div class="workspace-stage-context">
          <span class="workspace-stage-kicker">{{ kicker }}</span>
          <span v-if="status" class="workspace-stage-status" :class="`is-${tone}`">{{ status }}</span>
        </div>
        <div v-if="$slots.actions" class="workspace-stage-actions">
          <slot name="actions" />
        </div>
      </header>

      <h1>{{ title }}</h1>
      <p v-if="subtitle">{{ subtitle }}</p>

      <div v-if="$slots.default" class="workspace-stage-content">
        <slot />
      </div>
    </div>

    <aside v-if="$slots.aside" class="workspace-stage-aside">
      <slot name="aside" />
    </aside>
  </section>
</template>

<style scoped>
.workspace-stage {
  position: relative;
  min-height: 0;
  margin-bottom: 16px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(320px, 38%);
  overflow: hidden;
  border: 1px solid var(--ui-border);
  border-radius: var(--ui-radius-lg);
  background: var(--ui-surface);
  animation: workspace-stage-enter 240ms var(--ui-ease-out) both;
}

.workspace-stage-main,
.workspace-stage-aside {
  position: relative;
}

.workspace-stage.is-compact {
  min-height: 0;
  grid-template-columns: minmax(0, 1fr) minmax(390px, 38%);
  border-radius: var(--ui-radius-lg);
}

.workspace-stage.is-compact .workspace-stage-main {
  padding: 20px 24px;
}

.workspace-stage.is-compact h1 {
  margin-top: 9px;
  font-size: var(--ui-font-page-title);
  letter-spacing: -.02em;
}

.workspace-stage.is-compact p {
  margin-top: 5px;
  font-size: 12px;
}

.workspace-stage.is-compact .workspace-stage-content {
  padding-top: 16px;
}

.workspace-stage.is-compact .workspace-stage-aside {
  padding: 18px 20px;
}

.workspace-stage.is-compact :deep(.workspace-stage-stats) {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.workspace-stage.is-compact :deep(.workspace-stage-stats > div) {
  padding: 2px 14px;
  border-right: 1px solid var(--ui-border);
  border-bottom: 0;
}

.workspace-stage.is-compact :deep(.workspace-stage-stats > div:first-child) {
  padding-left: 0;
}

.workspace-stage.is-compact :deep(.workspace-stage-stats > div:last-child) {
  padding-right: 0;
  border-right: 0;
}

.workspace-stage.is-compact :deep(.workspace-stage-stats dd) {
  font-size: 13px;
}

.workspace-stage.is-compact :deep(.workspace-stage-pill) {
  min-height: 32px;
  padding: 0 10px;
}

.workspace-stage-main {
  min-width: 0;
  padding: 24px;
  display: flex;
  flex-direction: column;
}

.workspace-stage-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.workspace-stage-context,
.workspace-stage-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.workspace-stage-actions {
  flex-wrap: wrap;
  justify-content: flex-end;
}

.workspace-stage-kicker {
  color: var(--ui-primary);
  font: 600 var(--ui-font-caption)/1.2 var(--ui-font-mono);
  letter-spacing: .12em;
}

.workspace-stage-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ui-text-muted);
  font-size: var(--ui-font-caption);
}

.workspace-stage-status::before {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  content: "";
  box-shadow: 0 0 12px currentColor;
}

.workspace-stage-status.is-success { color: var(--ui-success); }
.workspace-stage-status.is-warning { color: var(--ui-warning); }
.workspace-stage-status.is-danger { color: var(--ui-danger); }

.workspace-stage h1 {
  margin: 12px 0 0;
  overflow: hidden;
  color: var(--ui-text);
  font-size: var(--ui-font-page-title);
  font-weight: 600;
  letter-spacing: -.02em;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workspace-stage p {
  max-width: 650px;
  margin: 12px 0 0;
  color: var(--ui-text-muted);
  font-size: 13px;
}

.workspace-stage-content {
  padding-top: 18px;
}

.workspace-stage-aside {
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  border-left: 1px solid var(--ui-border);
  background: color-mix(in srgb, var(--ui-surface-muted) 68%, var(--ui-surface));
}

:deep(.workspace-stage-pills) {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 9px;
}

:deep(.workspace-stage-pill) {
  min-height: 34px;
  padding: 0 12px;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  border: 1px solid var(--ui-border);
  border-radius: var(--ui-radius-md);
  background: color-mix(in srgb, var(--ui-surface-strong) 84%, transparent);
  color: var(--ui-text-muted);
  font-size: var(--ui-font-caption);
}

:deep(.workspace-stage-pill strong) {
  color: var(--ui-text);
  font: 12px "v-mono", monospace;
}

:deep(.workspace-stage-stats) {
  width: 100%;
  margin: 0;
  display: grid;
}

:deep(.workspace-stage-stats > div) {
  min-width: 0;
  padding: 14px 0;
  display: grid;
  gap: 5px;
  border-bottom: 1px solid var(--ui-border);
}

:deep(.workspace-stage-stats > div:last-child) {
  border-bottom: 0;
}

:deep(.workspace-stage-stats dt) {
  color: var(--ui-text-muted);
  font-size: var(--ui-font-caption);
}

:deep(.workspace-stage-stats dd) {
  margin: 0;
  overflow: hidden;
  color: var(--ui-text);
  font: 18px "v-mono", monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@keyframes workspace-stage-enter {
  from { opacity: 0; transform: translateY(10px) scale(.995); }
  to { opacity: 1; transform: translateY(0) scale(1); }
}

@media (max-width: 900px) {
  .workspace-stage {
    grid-template-columns: minmax(0, 1fr);
  }

  .workspace-stage-aside {
    padding: 14px 28px 22px;
    border-top: 1px solid var(--ui-border);
    border-left: 0;
  }

  :deep(.workspace-stage-stats) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  :deep(.workspace-stage-stats > div) {
    padding: 0 14px;
    border-right: 1px solid var(--ui-border);
    border-bottom: 0;
  }

  .workspace-stage.is-compact {
    grid-template-columns: minmax(0, 1fr);
  }

  .workspace-stage.is-compact .workspace-stage-aside {
    padding: 12px 24px 16px;
  }
}

@media (max-width: 600px) {
  .workspace-stage {
    min-height: 0;
  }

  .workspace-stage-main {
    padding: 18px 16px;
  }

  .workspace-stage-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .workspace-stage-actions {
    width: 100%;
    justify-content: flex-start;
  }

  .workspace-stage h1 {
    margin-top: 10px;
    font-size: 22px;
  }

  .workspace-stage p {
    line-height: 1.55;
  }

  .workspace-stage-aside,
  .workspace-stage.is-compact .workspace-stage-aside {
    padding: 14px 16px;
  }

  :deep(.workspace-stage-stats) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  :deep(.workspace-stage-stats > div) {
    padding: 2px 10px;
    border-right: 1px solid var(--ui-border);
    border-bottom: 0;
  }

  :deep(.workspace-stage-stats > div:first-child) {
    padding-left: 0;
  }

  :deep(.workspace-stage-stats > div:last-child) {
    padding-right: 0;
    border-right: 0;
  }

  :deep(.workspace-stage-stats dd) {
    font-size: var(--ui-font-caption);
  }
}

@media (prefers-reduced-motion: reduce) {
  .workspace-stage {
    animation-name: workspace-stage-fade;
  }

  @keyframes workspace-stage-fade {
    from { opacity: 0; }
    to { opacity: 1; }
  }
}
</style>
