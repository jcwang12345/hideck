<script setup lang="ts">
import type { AppError } from '../../types/domain'
import PageHeader from '../PageHeader.vue'
import RefreshButton from '../RefreshButton.vue'
import ErrorState from '../ErrorState.vue'
import EmptyState from '../EmptyState.vue'
import ListSkeleton from '../ListSkeleton.vue'
import { t } from '../../i18n'

export type ResourceColumn = {
  key: string
  label: string
  compact?: boolean
}

export type ResourceRow = {
  id: string
  title: string
  subtitle?: string
  status: string
  tone?: 'success' | 'warning' | 'danger' | 'muted' | 'info'
  values: Record<string, string>
}

defineProps<{
  title: string
  subtitle: string
  countLabel: string
  rows: ResourceRow[]
  columns: ResourceColumn[]
  loading: boolean
  error: AppError | null
  lastOkAt?: number | null
  emptyTitle: string
  emptySubtitle: string
}>()

const emit = defineEmits<{ (event: 'refresh'): void }>()
</script>

<template>
  <div class="app-page resource-page">
    <PageHeader :title="title" :subtitle="subtitle">
      <template #actions>
        <slot name="actions" />
        <RefreshButton :loading="loading" @click="emit('refresh')" />
      </template>
    </PageHeader>

    <ErrorState
      v-if="error"
      class="resource-error"
      :message="error.message"
      :status-code="error.status"
      :request-method="error.method"
      :request-url="error.url"
      :last-success-at="lastOkAt"
      :retry-text="t('resources.common.retry')"
      @retry="emit('refresh')"
    />

    <section class="resource-workspace ui-card ui-workspace-glow" :aria-label="title">
      <header class="resource-toolbar">
        <div>
          <span>{{ t('resources.common.control') }}</span>
          <strong>{{ countLabel }}</strong>
        </div>
        <small v-if="lastOkAt">{{ t('resources.common.synced') }}</small>
      </header>

      <ListSkeleton v-if="loading && rows.length === 0" :rows="7" />
      <EmptyState
        v-else-if="rows.length === 0"
        compact
        :title="emptyTitle"
        :subtitle="emptySubtitle"
      />

      <div v-else class="resource-table" role="table" :aria-label="title">
        <div class="resource-row resource-head" :class="{ 'has-actions': !!$slots['row-actions'] }" role="row">
          <span role="columnheader">{{ t('resources.common.resource') }}</span>
          <span role="columnheader">{{ t('resources.common.status') }}</span>
          <span v-for="column in columns" :key="column.key" role="columnheader">{{ column.label }}</span>
          <span v-if="$slots['row-actions']" role="columnheader">{{ t('resources.common.actions') }}</span>
        </div>
        <article v-for="row in rows" :key="row.id" class="resource-row" :class="{ 'has-actions': !!$slots['row-actions'] }" role="row">
          <div class="resource-identity" role="cell" :data-label="t('resources.common.resource')">
            <strong>{{ row.title }}</strong>
            <small v-if="row.subtitle">{{ row.subtitle }}</small>
          </div>
          <div role="cell" :data-label="t('resources.common.status')">
            <span class="resource-status" :class="`is-${row.tone || 'muted'}`">{{ row.status }}</span>
          </div>
          <div
            v-for="column in columns"
            :key="column.key"
            class="resource-value"
            :class="{ 'is-compact': column.compact }"
            role="cell"
            :data-label="column.label"
          >
            {{ row.values[column.key] || '—' }}
          </div>
          <div v-if="$slots['row-actions']" class="resource-row-actions" role="cell" :data-label="t('resources.common.actions')">
            <slot name="row-actions" :row="row" />
          </div>
        </article>
      </div>
    </section>
  </div>
</template>

<style scoped>
.resource-page { width: 100%; max-width: 1500px; margin: 0 auto; }
.resource-error { margin-bottom: 16px; }
.resource-workspace { min-width: 0; overflow: hidden; background: var(--ui-surface); }
.resource-toolbar {
  min-height: 68px;
  padding: 14px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border-bottom: 1px solid var(--ui-border);
  background: color-mix(in srgb, var(--ui-surface) 94%, var(--ui-bg));
}
.resource-toolbar > div { display: grid; gap: 3px; }
.resource-toolbar span { color: var(--ui-primary); font: 500 var(--ui-font-caption)/1.2 var(--ui-font-mono); letter-spacing: .08em; }
.resource-toolbar strong { color: var(--ui-text); font-size: 15px; font-weight: 600; }
.resource-toolbar small { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.resource-table { min-width: 760px; }
.resource-row {
  min-height: 68px;
  padding: 10px 18px;
  display: grid;
  grid-template-columns: minmax(210px, 1.3fr) minmax(112px, .65fr) repeat(v-bind('columns.length'), minmax(110px, 1fr));
  align-items: center;
  gap: 16px;
  border-bottom: 1px solid var(--ui-border-muted);
}
.resource-row.has-actions { grid-template-columns: minmax(210px, 1.3fr) minmax(112px, .65fr) repeat(v-bind('columns.length'), minmax(110px, 1fr)) minmax(160px, .8fr); }
.resource-row:last-child { border-bottom: 0; }
.resource-head { min-height: 42px; background: var(--ui-surface-muted); color: var(--ui-text-muted); font-size: var(--ui-font-caption); font-weight: 650; }
.resource-identity { min-width: 0; display: grid; gap: 3px; }
.resource-identity strong { overflow: hidden; color: var(--ui-text); font-size: var(--ui-font-body-sm); text-overflow: ellipsis; white-space: nowrap; }
.resource-identity small { overflow: hidden; color: var(--ui-text-muted); font-family: var(--ui-font-mono); font-size: var(--ui-font-caption); text-overflow: ellipsis; white-space: nowrap; }
.resource-value { min-width: 0; overflow-wrap: anywhere; color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.resource-value.is-compact { font-family: var(--ui-font-mono); font-size: var(--ui-font-caption); }
.resource-status { min-height: 28px; padding: 0 9px; display: inline-flex; align-items: center; border-radius: var(--ui-radius-pill); font-size: var(--ui-font-caption); font-weight: 650; }
.resource-status.is-success { background: var(--ui-success-surface); color: var(--ui-success); }
.resource-status.is-warning { background: var(--ui-warning-surface); color: var(--ui-warning); }
.resource-status.is-danger { background: var(--ui-danger-surface); color: var(--ui-danger); }
.resource-status.is-info { background: color-mix(in srgb, var(--ui-info) 12%, var(--ui-surface)); color: var(--ui-info); }
.resource-status.is-muted { background: var(--ui-surface-muted); color: var(--ui-text-muted); }
.resource-row-actions { min-width: 0; display: flex; align-items: center; justify-content: flex-end; flex-wrap: wrap; gap: 6px; }
.resource-row-actions :deep(.el-button) { min-height: 40px; margin: 0; }

@media (max-width: 900px) {
  .resource-workspace { background: transparent; border: 0; box-shadow: none; overflow: visible; }
  .resource-toolbar { margin-bottom: 10px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-lg); background: var(--ui-surface); }
  .resource-table { min-width: 0; display: grid; gap: 10px; }
  .resource-head { display: none; }
  .resource-row {
    min-height: 0;
    padding: 15px;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 13px 18px;
    border: 1px solid var(--ui-border);
    border-radius: var(--ui-radius-lg);
    background: var(--ui-surface);
    box-shadow: var(--ui-shadow-sm);
  }
  .resource-identity { grid-column: 1 / -1; padding-bottom: 10px; border-bottom: 1px solid var(--ui-border-muted); }
  .resource-row.has-actions { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .resource-row-actions { grid-column: 1 / -1; justify-content: stretch; padding-top: 4px; border-top: 1px solid var(--ui-border-muted); }
  .resource-row-actions :deep(.el-button) { flex: 1 1 120px; }
  .resource-row [role="cell"]::before { content: attr(data-label); margin-bottom: 4px; display: block; color: var(--ui-text-muted); font-size: var(--ui-font-caption); font-weight: 600; }
  .resource-identity::before { display: none !important; }
}

@media (max-width: 480px) {
  .resource-row { grid-template-columns: 1fr; }
  .resource-row.has-actions { grid-template-columns: 1fr; }
  .resource-toolbar { align-items: flex-start; }
}
</style>
