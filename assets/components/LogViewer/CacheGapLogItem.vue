<template>
  <div v-if="visible" class="text-base-content/60 my-3 flex w-full items-center gap-3 text-xs">
    <div class="bg-base-content/15 h-px flex-1"></div>
    <div class="badge badge-warning badge-outline rounded-sm px-3 py-2">
      {{ logEntry.message }}
    </div>
    <div class="bg-base-content/15 h-px flex-1"></div>
  </div>
</template>

<script lang="ts" setup>
import { CacheGapLogEntry } from "@/models/LogEntry";

defineProps<{
  logEntry: CacheGapLogEntry;
}>();

const visible = ref(true);
let hideTimer: ReturnType<typeof setTimeout> | undefined;

onMounted(() => {
  hideTimer = setTimeout(() => {
    visible.value = false;
  }, 3000);
});

onBeforeUnmount(() => {
  if (hideTimer) {
    clearTimeout(hideTimer);
  }
});
</script>
