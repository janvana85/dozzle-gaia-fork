<template>
  <fieldset class="fieldset">
    <legend class="fieldset-legend text-lg">{{ $t(labelKey) }}</legend>
    <div class="flex gap-2">
      <input v-model.number="amount" type="number" min="0" class="input focus:input-primary w-32" />
      <select v-model.number="unit" class="select focus:select-primary w-36">
        <option :value="1">{{ $t("notifications.alert-form.duration-seconds") }}</option>
        <option :value="60">{{ $t("notifications.alert-form.duration-minutes") }}</option>
        <option :value="3600">{{ $t("notifications.alert-form.duration-hours") }}</option>
        <option :value="86400">{{ $t("notifications.alert-form.duration-days") }}</option>
      </select>
    </div>
    <p class="text-base-content/50 mt-1 text-xs">
      <template v-if="model === 0">{{ $t("notifications.alert-form.no-cooldown") }}</template>
      <template v-else>{{ $t(hintKey, { duration: formatDuration(model, locale || undefined) }) }}</template>
    </p>
    <p v-if="explanationKey" class="text-base-content/50 mt-1 text-xs">{{ $t(explanationKey) }}</p>
  </fieldset>
</template>

<script lang="ts" setup>
withDefaults(
  defineProps<{
    labelKey?: string;
    hintKey?: string;
    explanationKey?: string;
  }>(),
  {
    labelKey: "notifications.alert-form.cooldown-label",
    hintKey: "notifications.alert-form.cooldown-hint",
    explanationKey: "notifications.alert-form.cooldown-explanation",
  },
);

const maxCooldown = 48 * 60 * 60;
const model = defineModel<number>({ required: true });

function bestUnit(seconds: number) {
  if (seconds > 0 && seconds % 86400 === 0) return 86400;
  if (seconds > 0 && seconds % 3600 === 0) return 3600;
  if (seconds > 0 && seconds % 60 === 0) return 60;
  return 1;
}

const unit = ref(bestUnit(model.value));
const amount = ref(Math.floor((model.value || 0) / unit.value));

watch([amount, unit], () => {
  const next = Math.min(Math.max(0, Number(amount.value || 0) * unit.value), maxCooldown);
  if (next !== model.value) {
    model.value = next;
  }
});

watch(model, (value) => {
  const nextUnit = bestUnit(value);
  unit.value = nextUnit;
  amount.value = Math.floor((value || 0) / nextUnit);
});
</script>
