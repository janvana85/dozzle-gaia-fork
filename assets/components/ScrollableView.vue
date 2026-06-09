<template>
  <section :class="{ 'h-screen min-h-0': scrollable }" class="flex flex-col">
    <header
      v-if="$slots.header"
      class="border-base-content/10 bg-base-200 sticky top-[calc(55px+env(safe-area-inset-top))] z-20 border-b py-0.5 shadow-[1px_1px_2px_0_rgb(0,0,0,0.05)] md:top-0 md:py-2"
    >
      <slot name="header"></slot>
    </header>
    <div
      v-if="!historical"
      class="border-base-content/10 bg-base-100 sticky top-[calc(55px+env(safe-area-inset-top)+3rem)] z-10 flex items-center justify-between border-b px-4 py-2 text-sm md:top-[3.25rem]"
    >
      <div class="flex items-center gap-2">
        <span class="badge badge-outline" :class="cacheModeClass">
          {{ cacheModeLabel }}
        </span>
        <span class="text-base-content/60" v-if="isSearching && followingSearch"
          >following live (filtered) for {{ followRemainingLabel }}</span
        >
        <span class="text-base-content/60" v-else-if="isSearching">search snapshot</span>
        <span class="text-base-content/60" v-else-if="!followLogs">paused at history</span>
        <span class="text-base-content/60" v-else>following live for {{ followRemainingLabel }}</span>
      </div>
      <button class="btn btn-primary btn-sm" @click="toggleFollowLogs" v-if="!isSearching">
        {{ followLogs ? "Pause follow" : "Follow logs for 5m" }}
      </button>
    </div>
    <main
      ref="scrollRoot"
      :data-scrolling="scrollable ? true : undefined"
      class="min-h-[300px] snap-y overflow-auto"
      @scroll.passive="handleScroll"
    >
      <div class="invisible relative md:visible" v-show="scrollContext.paused && showScrollProgress">
        <div class="absolute top-4 right-44">
          <ScrollProgress
            :indeterminate="loadingMore"
            :auto-hide="!loadingMore"
            :progress="scrollContext.progress"
            :date="scrollContext.currentDate"
            class="fixed! z-10 min-w-40"
          />
        </div>
      </div>
      <div ref="scrollableContent">
        <slot></slot>
      </div>

      <div ref="scrollObserver" class="h-px"></div>
    </main>

    <div class="mr-16 text-right" v-if="!historical">
      <transition name="fade">
        <button
          class="btn btn-primary text-primary-content fixed bottom-8 rounded-sm p-3 shadow-sm transition-colors"
          :class="hasMore ? 'btn-secondary animate-bounce-fast text-secondary-content' : ''"
          @click="scrollToBottom()"
          v-show="scrollContext.paused"
        >
          <mdi:chevron-double-down />
        </button>
      </transition>
    </div>

    <!-- During a search snapshot, offer to follow new matching logs live -->
    <div class="mr-16 text-right" v-if="!historical">
      <transition name="fade">
        <button
          class="btn btn-secondary text-secondary-content animate-bounce-fast fixed right-20 bottom-8 gap-2 rounded-sm p-3 shadow-sm"
          @click="followSearchResults()"
          v-show="isSearching && !followingSearch && pendingSearchCount > 0"
        >
          <mdi:chevron-double-down />
          <span class="text-sm">{{ pendingSearchCount }} new — follow live</span>
        </button>
      </transition>
    </div>
  </section>
</template>

<script lang="ts" setup>
const { scrollable = false } = defineProps<{ scrollable?: boolean }>();

const followDurationMs = 5 * 60 * 1000;
const hasMore = ref(false);
const followLogs = ref(true);
const followUntil = ref(Date.now() + followDurationMs);
const now = ref(Date.now());
const scrollRoot = ref<HTMLElement>();
const scrollObserver = ref<HTMLElement>();
const scrollableContent = ref<HTMLElement>();

const scrollContext = provideScrollContext();
const { cached, loadingMore, historical, cacheMode, containers } = useLoggingContext();
const { isSearching, followingSearch, pendingSearchCount, startFollowingSearch } = useSearchFilter();

const cacheModeLabel = computed(() => {
  if (cacheMode.value === "mixed") return "live + cache";
  if (cacheMode.value === "cache") return "cache";
  return "live";
});
const cacheModeClass = computed(() => {
  if (cacheMode.value === "mixed") return "badge-info";
  if (cacheMode.value === "cache") return "badge-warning";
  return "badge-success";
});
const showScrollProgress = computed(() => containers.value.length === 1 && scrollContext.hasProgress);
const followRemainingMs = computed(() => Math.max(0, followUntil.value - now.value));
const followRemainingLabel = computed(() => {
  const totalSeconds = Math.ceil(followRemainingMs.value / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
});

let followTimer: ReturnType<typeof setInterval> | undefined;
let programmaticScrollTimer: ReturnType<typeof setTimeout> | undefined;
let followScrollFrame: number | undefined;
let programmaticScroll = false;
let previousScrollTop = 0;

if (!historical.value) {
  followTimer = setInterval(() => {
    now.value = Date.now();
    if (followLogs.value && now.value >= followUntil.value) {
      followLogs.value = false;
    }
  }, 1000);

  useIntersectionObserver(
    scrollObserver,
    ([entry]) => {
      scrollContext.paused = entry.intersectionRatio == 0;
      if (!scrollContext.paused) {
        programmaticScroll = false;
        if (programmaticScrollTimer) clearTimeout(programmaticScrollTimer);
      } else if (followLogs.value && !programmaticScroll) {
        followLogs.value = false;
      }
    },
    {
      threshold: [0, 1],
      rootMargin: "40px 0px",
    },
  );
  useEventListener(window, "scroll", handleScroll, { passive: true });

  useMutationObserver(
    scrollableContent,
    (records) => {
      if (followLogs.value && (!scrollContext.paused || programmaticScroll)) {
        scheduleFollowScroll();
      } else {
        const record = records[records.length - 1];
        const children = (record.target as HTMLElement).children;
        if (children[children.length - 1] == record.addedNodes[record.addedNodes.length - 1]) {
          hasMore.value = true;
        }
      }
    },
    { childList: true, subtree: true },
  );
}

onMounted(() => {
  if (historical.value) return;
  followUntil.value = Date.now() + followDurationMs;
  now.value = Date.now();
  followLogs.value = true;
  programmaticScroll = true;
  nextTick(() => scrollToBottom());
});

onScopeDispose(() => {
  if (followTimer) {
    clearInterval(followTimer);
  }
  if (programmaticScrollTimer) {
    clearTimeout(programmaticScrollTimer);
  }
  if (followScrollFrame !== undefined) {
    cancelAnimationFrame(followScrollFrame);
  }
});

function toggleFollowLogs() {
  if (followLogs.value) {
    followLogs.value = false;
    return;
  }
  followUntil.value = Date.now() + followDurationMs;
  now.value = Date.now();
  followLogs.value = true;
  scrollToBottom("smooth");
}

function scrollToBottom(behavior: "auto" | "smooth" = "auto") {
  programmaticScroll = true;
  if (programmaticScrollTimer) clearTimeout(programmaticScrollTimer);
  programmaticScrollTimer = setTimeout(
    () => {
      programmaticScroll = false;
    },
    behavior === "smooth" ? 1000 : 100,
  );
  const root = scrollContainer();
  if (root) {
    if (root === document.scrollingElement) {
      // Chromium can ignore Element.scrollTo({ behavior: "smooth" }) on the
      // root element while the page height is changing. A direct window scroll
      // keeps live follow pinned deterministically.
      window.scrollTo(0, root.scrollHeight);
    } else {
      root.scrollTo?.({ top: root.scrollHeight, behavior });
    }
    previousScrollTop = root.scrollTop;
  }
  hasMore.value = false;
}

function scheduleFollowScroll() {
  if (followScrollFrame !== undefined) return;
  followScrollFrame = requestAnimationFrame(() => {
    followScrollFrame = undefined;
    const root = scrollContainer();
    if (!root || !followLogs.value || (scrollContext.paused && !programmaticScroll)) return;
    const bottomGap = root.scrollHeight - root.clientHeight - root.scrollTop;
    if (bottomGap > 1) {
      scrollToBottom();
    }
  });
}

function scrollContainer() {
  return scrollRoot.value && scrollRoot.value.scrollHeight > scrollRoot.value.clientHeight
    ? scrollRoot.value
    : document.scrollingElement;
}

function handleScroll() {
  const root = scrollContainer();
  if (!root) return;

  const previous = previousScrollTop;
  previousScrollTop = root.scrollTop;
  if (historical.value || !followLogs.value || programmaticScroll) return;

  const scrollingUp = root.scrollTop < previous - 24;
  const awayFromBottom = root.scrollHeight - root.clientHeight - root.scrollTop > 40;
  if (scrollingUp && awayFromBottom) {
    followLogs.value = false;
  }
}

// Switch a search from the static snapshot to live-following the filtered stream.
function followSearchResults() {
  followUntil.value = Date.now() + followDurationMs;
  now.value = Date.now();
  followLogs.value = true;
  startFollowingSearch();
  scrollToBottom("smooth");
}

defineExpose({
  followLogs,
  cached,
});
</script>
<style scoped>
.fade-enter-active,
.fade-leave-active {
  @apply transition-opacity;
}

.fade-enter-from,
.fade-leave-to {
  @apply opacity-0;
}
</style>

<style>
.splitpanes__pane {
  overflow: unset !important;
}
</style>
