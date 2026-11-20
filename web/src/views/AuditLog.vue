<script setup lang="ts">
import { computed } from 'vue'
import ResourceCollectionView, { type ResourceColumn, type ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import { useResourceCollection } from '../composables/useResourceCollection'
import { adminResourcesService } from '../services/admin-resources'
import { displayDate, displayStatus, displayValue, statusTone } from '../utils/resourcePresentation'
import { t } from '../i18n'

const state = useResourceCollection(adminResourcesService.auditEvents)
const columns = computed<ResourceColumn[]>(() => [
  { key: 'actor', label: t('resources.audit.actor') },
  { key: 'resource', label: t('resources.audit.resource') },
  { key: 'message', label: t('resources.audit.message') },
  { key: 'time', label: t('resources.audit.time'), compact: true }
])
const rows = computed<ResourceRow[]>(() => state.items.value.map((event, index) => {
  const result = displayStatus(event.result, t('resources.common.recorded'))
  return {
    id: displayValue(event.id, `event-${index}`),
    title: displayValue(event.action, t('resources.common.systemAction')),
    subtitle: displayValue(event.message, t('resources.common.auditEvent')),
    status: result,
    tone: statusTone(event.result),
    values: {
      actor: displayValue(event.actor_name || event.actor_id, t('resources.common.system')),
      resource: `${displayValue(event.resource_type, t('resources.common.genericResource'))} · ${displayValue(event.resource_id)}`,
      message: displayValue(event.message),
      time: displayDate(event.created_at)
    }
  }
}))
</script>

<template>
  <ResourceCollectionView
    :title="t('resources.audit.title')"
    :subtitle="t('resources.audit.subtitle')"
    :count-label="t('resources.audit.count', { count: rows.length })"
    :rows="rows"
    :columns="columns"
    :loading="state.loading.value"
    :error="state.error.value"
    :last-ok-at="state.lastOkAt.value"
    :empty-title="t('resources.audit.empty')"
    :empty-subtitle="t('resources.audit.emptyHint')"
    @refresh="state.load"
  />
</template>
