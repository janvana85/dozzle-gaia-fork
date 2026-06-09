<template>
  <LogList :messages="visibleMessages" />
</template>

<script lang="ts" setup>
import { type JSONObject, type LogMessage, LogEntry } from "@/models/LogEntry";

const props = defineProps<{
  messages: LogEntry<string | string[] | JSONObject>[];
  visibleKeys: Map<string[], boolean>;
}>();

const { messages, visibleKeys } = toRefs(props);

const { filteredPayload } = useVisibleFilter(visibleKeys);
const filteredMessages = filteredPayload(messages);
const visibleMessages = shallowRef<LogEntry<LogMessage>[]>([]);

const INITIAL_RENDER_COUNT = 300;
const RENDER_BATCH_SIZE = 200;

let renderToken = 0;
let cancelPendingRender: (() => void) | undefined;

function scheduleRender(callback: () => void) {
  const idleWindow = window as Partial<Window>;
  if (idleWindow.requestIdleCallback && idleWindow.cancelIdleCallback) {
    const id = idleWindow.requestIdleCallback(callback, { timeout: 100 });
    return () => idleWindow.cancelIdleCallback?.(id);
  }

  const id = globalThis.setTimeout(callback, 0);
  return () => globalThis.clearTimeout(id);
}

function scheduleChunkedRender(source: LogEntry<LogMessage>[]) {
  const token = ++renderToken;
  cancelPendingRender?.();
  cancelPendingRender = undefined;

  const total = source.length;
  // Chunk only the first large render. Once the full live window is visible,
  // resetting to the first 300 rows for every incoming log briefly removes the
  // newest rows and makes follow mode flash several minutes backwards.
  if (total <= INITIAL_RENDER_COUNT || visibleMessages.value.length > INITIAL_RENDER_COUNT) {
    visibleMessages.value = source;
    return;
  }

  let index = INITIAL_RENDER_COUNT;
  visibleMessages.value = source.slice(0, index);

  const pump = () => {
    if (token !== renderToken) return;
    const nextIndex = Math.min(index + RENDER_BATCH_SIZE, total);
    visibleMessages.value = source.slice(0, nextIndex);
    index = nextIndex;
    if (index < total) {
      cancelPendingRender = scheduleRender(pump);
    } else {
      cancelPendingRender = undefined;
    }
  };

  cancelPendingRender = scheduleRender(pump);
}

watch(
  filteredMessages,
  (value) => {
    scheduleChunkedRender(value);
  },
  { immediate: true },
);

onBeforeUnmount(() => cancelPendingRender?.());
</script>
<style scoped></style>
