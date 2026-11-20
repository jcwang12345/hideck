<script setup lang="ts">
defineProps<{
  modelValue: boolean
  title: string
  subtitle?: string
  busy?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    class="resource-editor-drawer"
    direction="rtl"
    size="min(580px, 100%)"
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    destroy-on-close
    @close="emit('update:modelValue', false)"
  >
    <template #header>
      <div class="resource-editor-heading">
        <strong>{{ title }}</strong>
        <p v-if="subtitle">{{ subtitle }}</p>
      </div>
    </template>
    <div class="resource-editor-body">
      <slot />
    </div>
    <template #footer>
      <div class="resource-editor-footer">
        <el-button :disabled="busy" @click="emit('update:modelValue', false)">取消</el-button>
        <slot name="actions" />
      </div>
    </template>
  </el-drawer>
</template>

<style>
.resource-editor-drawer .el-drawer__header { margin: 0; padding: 20px 22px 16px; border-bottom: 1px solid var(--ui-border); }
.resource-editor-drawer .el-drawer__body { padding: 0; }
.resource-editor-drawer .el-drawer__footer { padding: 14px 22px calc(14px + env(safe-area-inset-bottom)); border-top: 1px solid var(--ui-border); }
.resource-editor-heading { min-width: 0; }
.resource-editor-heading strong { color: var(--ui-text); font-size: 19px; }
.resource-editor-heading p { margin: 5px 0 0; color: var(--ui-text-muted); font-size: 13px; line-height: 1.5; }
.resource-editor-body { padding: 20px 22px 28px; display: grid; gap: 18px; }
.resource-editor-footer { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 10px; }
.resource-editor-footer .el-button { min-height: 44px; margin: 0; }
.resource-editor-form { display: grid; gap: 16px; }
.resource-editor-form .el-form-item { margin: 0; }
.resource-editor-form .el-input__wrapper,
.resource-editor-form .el-select__wrapper,
.resource-editor-form .el-textarea__inner { min-height: 44px; }
.resource-editor-form .el-input-number { width: 100%; }
.resource-editor-form .el-input-number .el-input__wrapper { min-height: 44px; }
.resource-editor-section { padding: 16px; display: grid; gap: 13px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-lg); background: var(--ui-surface-muted); }
.resource-editor-section > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.resource-editor-section > header strong { color: var(--ui-text); font-size: 14px; }
.resource-editor-section > header p { margin: 3px 0 0; color: var(--ui-text-muted); font-size: 12px; line-height: 1.45; }
.resource-choice-list { display: grid; gap: 8px; }
.resource-choice-row { min-width: 0; padding: 11px 12px; display: grid; gap: 9px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface); }
.resource-choice-row-main { min-width: 0; display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.resource-choice-row-main strong { overflow-wrap: anywhere; color: var(--ui-text); font-size: 13px; }
.resource-choice-row-main small { display: block; margin-top: 3px; overflow-wrap: anywhere; color: var(--ui-text-muted); font-size: 12px; }
.resource-choice-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.resource-form-error { margin: 0; color: var(--ui-danger); font-size: 12px; line-height: 1.45; }
.resource-form-hint { margin: 0; color: var(--ui-text-muted); font-size: 12px; line-height: 1.5; }

@media (max-width: 600px) {
  .resource-editor-drawer .el-drawer__header { padding: 16px 16px 13px; }
  .resource-editor-drawer .el-drawer__footer { padding-inline: 16px; }
  .resource-editor-body { padding: 16px 16px 24px; }
  .resource-editor-footer .el-button { flex: 1 1 120px; }
  .resource-choice-options { grid-template-columns: 1fr; }
}
</style>
