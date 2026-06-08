<template>
  <ul class="group pt-4" :class="{ 'disable-wrap': !softWrap, [size]: true, compact }" data-logs>
    <li
      v-for="item in renderableMessages"
      ref="list"
      v-memo="[entryKey(item), (item as any).state, (item as any).isNew, (item as any).health]"
      :key="entryKey(item)"
      :id="entryDomId(item)"
      :data-time="item.date.getTime()"
      class="group/entry"
      :class="{ 'log-permalink-target': permalinkLogId === item.id.toString() }"
    >
      <component :is="item.getComponent()" :log-entry="item" />
    </li>
  </ul>
</template>

<script lang="ts" setup>
import {
  CacheGapLogEntry,
  LoadMoreLogEntry,
  type LogEntry,
  type LogMessage,
  SkippedLogsEntry,
} from "@/models/LogEntry";

const scrollContext = useScrollContext() as Partial<{
  progress: Ref<number>;
  currentDate: Ref<Date>;
  hasProgress: Ref<boolean>;
}>;
const progress = scrollContext.progress ?? ref(1);
const currentDate = scrollContext.currentDate ?? ref(new Date());
const hasProgress = scrollContext.hasProgress ?? ref(false);

const { messages } = defineProps<{
  messages: LogEntry<LogMessage>[];
}>();

const { containers } = useLoggingContext();

// Defensive: never render undefined/null holes (which would throw on item.id).
// Holes can slip into the merged stream from concurrent loaders; this guards the
// render regardless of the source.
const renderableMessages = computed(() => messages.filter((m) => m != null));

const route = useRoute();
const permalinkLogId = computed(() => (typeof route?.query?.logId === "string" ? route.query.logId : ""));
const multipleContainers = computed(() => containers.value.length > 1);
const realMessages = computed(() =>
  renderableMessages.value.filter(
    (message) =>
      !(message instanceof LoadMoreLogEntry) &&
      !(message instanceof SkippedLogsEntry) &&
      !(message instanceof CacheGapLogEntry),
  ),
);
const enableProgressTracking = computed(
  () => containers.value.length === 1 && realMessages.value.length > 1 && realMessages.value.length <= 1500,
);
const progressTargets = computed(() => (enableProgressTracking.value ? list.value : []));

const list = ref<HTMLElement[]>([]);

function entryKey(item: LogEntry<LogMessage>) {
  return `${item.containerID || "system"}:${item.id}`;
}

function entryDomId(item: LogEntry<LogMessage>) {
  if (!multipleContainers.value) {
    return item.id.toString();
  }
  return `${item.containerID || "system"}-${item.id}`;
}

let previousDate = new Date();
watchEffect(() => {
  hasProgress.value = enableProgressTracking.value;
  if (!hasProgress.value) {
    progress.value = 1;
    currentDate.value = new Date();
  }
});

useIntersectionObserver(
  progressTargets,
  (entries) => {
    if (!enableProgressTracking.value) return;
    const firstLog = realMessages.value[0];
    const lastLog = realMessages.value.at(-1);
    if (!firstLog || !lastLog) return;
    const totalSpan = lastLog.date.getTime() - firstLog.date.getTime();
    if (totalSpan <= 0) return;
    for (const entry of entries) {
      if (entry.isIntersecting) {
        const time = entry.target.getAttribute("data-time");
        if (time) {
          const date = new Date(parseInt(time));
          if (+date === +previousDate) break;
          previousDate = date;
          progress.value = Math.min(1, Math.max(0, (date.getTime() - firstLog.date.getTime()) / totalSpan));
          currentDate.value = date;
          break;
        }
      }
    }
  },
  {
    rootMargin: "-10% 0px -10% 0px",
    threshold: 1,
  },
);
</script>
<style scoped>
@reference "@/main.css";
ul {
  font-family:
    ui-monospace,
    SFMono-Regular,
    SF Mono,
    Consolas,
    Liberation Mono,
    monaco,
    Menlo,
    monospace;

  > li {
    @apply flex px-2 py-1 break-words last:snap-end odd:bg-gray-400/[0.07] md:px-4;
    contain: layout style paint;
    content-visibility: auto;
    contain-intrinsic-size: auto 28px;
    &:last-child {
      scroll-margin-block-end: 5rem;
    }

    &.log-permalink-target {
      @apply bg-secondary/15 border-secondary -ml-1 border-l-4 pl-3;
      animation: log-permalink-pulse 1.4s ease-out;
    }
  }

  &.small {
    @apply text-[0.7em];
  }

  &.medium {
    @apply text-[0.8em];
  }

  &.large {
    @apply text-[1em];
  }

  &.compact {
    > li {
      @apply py-0;
    }

    :deep(.tag) {
      @apply rounded-none;
    }
  }

  :deep(mark) {
    @apply bg-secondary inline-block rounded-xs;
    animation: pops 200ms ease-out;
  }

  :deep(a[rel~="external"]) {
    @apply text-primary underline-offset-4 hover:underline;
  }
}

@keyframes pops {
  0% {
    transform: scale(1.5);
  }
  100% {
    transform: scale(1.05);
  }
}

@keyframes log-permalink-pulse {
  0% {
    background-color: var(--color-secondary);
  }
  100% {
    /* Settle to the resting bg-secondary/15 declared on the .li above. */
    background-color: color-mix(in oklab, var(--color-secondary) 15%, transparent);
  }
}
</style>
