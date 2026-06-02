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

function scheduleChunkedRender(source: LogEntry<LogMessage>[]) {
  const token = ++renderToken;
  visibleMessages.value = [];

  const total = source.length;
  if (total <= INITIAL_RENDER_COUNT) {
    visibleMessages.value = source;
    return;
  }

  let index = 0;
  const pump = () => {
    if (token !== renderToken) return;
    const nextIndex = Math.min(index + (index === 0 ? INITIAL_RENDER_COUNT : RENDER_BATCH_SIZE), total);
    visibleMessages.value = source.slice(0, nextIndex);
    index = nextIndex;
    if (index < total) {
      requestAnimationFrame(pump);
    }
  };

  requestAnimationFrame(pump);
}

watch(
  filteredMessages,
  (value) => {
    scheduleChunkedRender(value);
  },
  { immediate: true },
);
</script>
<style scoped></style>
