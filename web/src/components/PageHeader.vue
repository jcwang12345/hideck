<script setup lang="ts">
defineProps<{
  title: string
  subtitle?: string
}>()
</script>

<template>
  <header class="page-header">
    <div class="page-heading">
      <div>
        <span class="page-heading-kicker">HIDECK / CONTROL PLANE</span>
        <h1>{{ title }}</h1>
        <p v-if="subtitle">{{ subtitle }}</p>
      </div>
    </div>
    <div class="page-actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.page-header {
  min-height: 52px;
  margin-bottom: 18px;
  padding-bottom: 14px;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--ui-space-5);
  border-bottom: 1px solid var(--ui-border);
}

.page-heading {
  min-width: 0;
  display: flex;
  align-items: flex-start;
  gap: 0;
}

h1 {
  margin: 3px 0 0;
  color: var(--ui-text);
  font-size: var(--ui-font-page-title);
  font-weight: 600;
  line-height: 1.2;
  letter-spacing: 0;
}

.page-heading-kicker {
  color: var(--ui-primary);
  font-family: var(--ui-font-mono);
  font-size: var(--ui-font-caption);
  font-weight: 500;
  letter-spacing: .08em;
}

p {
  margin: 5px 0 0;
  color: var(--ui-text-muted);
  font-size: 13px;
  line-height: 1.4;
}

.page-actions {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--ui-space-2);
}

@media (max-width: 640px) {
  .page-header {
    min-height: auto;
    margin-bottom: 14px;
    padding-bottom: 12px;
    align-items: stretch;
    flex-direction: column;
    gap: var(--ui-space-3);
  }

  h1 {
    font-size: 20px;
  }

  .page-actions {
    width: 100%;
    flex-wrap: wrap;
  }

  .page-actions :deep(> *) {
    max-width: 100%;
    flex-wrap: wrap;
  }
}
</style>
