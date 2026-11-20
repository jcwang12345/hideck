<script setup lang="ts">
import { computed } from 'vue'
import ResourceCollectionView, { type ResourceColumn, type ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import { useResourceCollection } from '../composables/useResourceCollection'
import { adminResourcesService } from '../services/admin-resources'
import { displayDate, displayDuration, displayStatus, displayValue, statusTone } from '../utils/resourcePresentation'
import { t } from '../i18n'

const state = useResourceCollection(adminResourcesService.callRecords)
const columns = computed<ResourceColumn[]>(() => [
  { key: 'owner', label: t('resources.calls.owner') },
  { key: 'line', label: t('resources.calls.line') },
  { key: 'duration', label: t('resources.calls.duration') },
  { key: 'started', label: t('resources.calls.started'), compact: true }
])
const rows = computed<ResourceRow[]>(() => state.items.value.map((record, index) => {
  const status = displayStatus(record.status, t('resources.common.unknownResult'))
  const peer = displayValue(record.peer, t('resources.common.unknownNumber'))
  return {
    id: displayValue(record.id || record.call_id, `call-${index}`),
    title: `${record.direction === 'inbound' ? t('resources.common.inbound') : t('resources.common.outbound')} · ${peer}`,
    subtitle: displayValue(record.end_reason, `Call ${displayValue(record.call_id)}`),
    status,
    tone: statusTone(record.status),
    values: {
      owner: displayValue(record.username || record.user_id, t('resources.common.currentUser')),
      line: `${displayValue(record.pool_name, t('resources.common.defaultLine'))} · ${displayValue(record.local_number)}`,
      duration: displayDuration(record.duration_seconds),
      started: displayDate(record.started_at)
    }
  }
}))
</script>

<template>
  <ResourceCollectionView
    :title="t('resources.calls.title')"
    :subtitle="t('resources.calls.subtitle')"
    :count-label="t('resources.calls.count', { count: rows.length })"
    :rows="rows"
    :columns="columns"
    :loading="state.loading.value"
    :error="state.error.value"
    :last-ok-at="state.lastOkAt.value"
    :empty-title="t('resources.calls.empty')"
    :empty-subtitle="t('resources.calls.emptyHint')"
    @refresh="state.load"
  />
</template>
