import { ShallowRef, type Ref } from "vue";

import debounce from "lodash.debounce";
import {
  type LogEvent,
  type JSONObject,
  type LogMessage,
  LogEntry,
  asLogEntry,
  ContainerEventLogEntry,
  ComplexLogEntry,
  SkippedLogsEntry,
  LoadMoreLogEntry,
} from "@/models/LogEntry";
import { Service, Stack } from "@/models/Stack";
import { Container, GroupedContainers } from "@/models/Container";
import { parseMessage } from "@/composable/loadBetween";
import { useLogLoader } from "@/composable/logLoader";
import { parseEventData } from "@/utils/events";

const { isSearching, debouncedSearchFilter, inverseFilter, followingSearch, pendingSearchCount } = useSearchFilter();
const mergedLiveLateToleranceMs = 5_000;

export function useContainerStream(container: Ref<Container>): LogStreamSource {
  const url = computed(() => `/api/hosts/${container.value.host}/containers/${container.value.id}/logs/stream`);
  return useLogStream(url, container);
}

export function useHostStream(host: Ref<Host>): LogStreamSource {
  return useLogStream(computed(() => `/api/hosts/${host.value.id}/logs/stream`));
}

export function useHostGroupStream(group: Ref<{ name: string }>): LogStreamSource {
  return useLogStream(computed(() => `/api/host-groups/${encodeURIComponent(group.value.name)}/logs/stream`));
}

export function useStackStream(stack: Ref<Stack>): LogStreamSource {
  const labels = computed(() => `com.docker.stack.namespace:${stack.value.name}`);
  return useLogStream(computed(() => `/api/labels/${labels.value}/logs/stream`));
}

export function useGroupedStream(group: Ref<GroupedContainers>): LogStreamSource {
  return useLogStream(computed(() => `/api/groups/${group.value.name}/logs/stream`));
}

export function useMergedStream(containers: Ref<Container[]>): LogStreamSource {
  const url = computed(() => {
    if (containers.value.length === 0) {
      return undefined;
    }
    const ids = containers.value.map((c) => c.id).join(",");
    return `/api/hosts/${containers.value[0].host}/logs/mergedStream/${ids}`;
  });

  return useLogStream(url);
}

export function useServiceStream(service: Ref<Service>): LogStreamSource {
  const labels = computed(() => `com.docker.swarm.service.name:${service.value.name}`);
  return useLogStream(computed(() => `/api/labels/${labels.value}/logs/stream`));
}

export function useNamespaceStream(namespace: Ref<{ name: string }>): LogStreamSource {
  const labels = computed(() => `namespace:${namespace.value.name}`);
  return useLogStream(computed(() => `/api/labels/${labels.value}/logs/stream`));
}

export function useOwnerStream(owner: Ref<{ name: string; kind: string }>): LogStreamSource {
  const labels = computed(() => `owner.kind:${owner.value.kind},owner.name:${owner.value.name}`);
  return useLogStream(computed(() => `/api/labels/${labels.value}/logs/stream`));
}

export type SearchStatus = {
  active: boolean;
  done: boolean;
  matches: number;
  scannedTo?: string;
  reason?: "capped" | "exhausted";
};

export type LogStreamSource = ReturnType<typeof useLogStream>;

function useLogStream(url: Ref<string | undefined>, container?: Ref<Container>) {
  const messages: ShallowRef<LogEntry<LogMessage>[]> = shallowRef([]);
  const buffer: ShallowRef<LogEntry<LogMessage>[]> = shallowRef([]);
  const opened = ref(false);
  const loading = ref(true);
  const error = ref(false);
  const searchStatus = ref<SearchStatus>({ active: false, done: false, matches: 0 });
  const { paused: scrollingPaused } = useScrollContext();
  const loggingContext = useLoggingContext();
  const { streamConfig, hasComplexLogs, levels, loadingMore, containers } = loggingContext;
  const cached = loggingContext.cached ?? ref(false);
  const cacheMode = loggingContext.cacheMode ?? ref<"live" | "cache" | "mixed">("live");
  let initial = true;

  const params = computed(() => {
    const params = new URLSearchParams();
    if (streamConfig.value.stdout) params.append("stdout", "1");
    if (streamConfig.value.stderr) params.append("stderr", "1");
    if (isSearching.value) {
      params.append("filter", debouncedSearchFilter.value);
      if (inverseFilter.value) params.append("inverse", "true");
    }
    for (const level of levels.value) {
      params.append("levels", level);
    }
    return params;
  });

  const allContainers = computed(() => (container ? [container.value] : containers.value));
  const { loadOlderLogs, loadSkippedLogs, loadSearchResults } = useLogLoader(
    messages,
    allContainers,
    params,
    loadingMore,
  );

  function mergeChronologically(existing: LogEntry<LogMessage>[], incoming: LogEntry<LogMessage>[]) {
    const loader = existing[0] instanceof LoadMoreLogEntry ? existing[0] : undefined;
    const rest = loader ? existing.slice(1) : existing;
    const byKey = new Map<string, LogEntry<LogMessage>>();
    for (const log of [...rest, ...incoming]) {
      const key = `${log.containerID ?? ""}:${log.id}`;
      if (!byKey.has(key)) {
        byKey.set(key, log);
      }
    }
    const merged = [...byKey.values()].sort((a, b) => a.date.getTime() - b.date.getTime());
    return loader ? [loader, ...merged] : merged;
  }

  function filterLateMergedLiveLogs(existing: LogEntry<LogMessage>[], incoming: LogEntry<LogMessage>[]) {
    if (scrollingPaused.value || containers.value.length <= 1 || incoming.length === 0) {
      return incoming;
    }
    const rest = existing[0] instanceof LoadMoreLogEntry ? existing.slice(1) : existing;
    const latestVisible = rest.at(-1)?.date.getTime();
    if (!latestVisible) {
      return incoming;
    }
    return incoming.filter((log) => log.date.getTime() >= latestVisible - mergedLiveLateToleranceMs);
  }

  function flushNow() {
    const liveIncoming = filterLateMergedLiveLogs(messages.value, buffer.value);
    if (messages.value.length + buffer.value.length > config.maxLogs) {
      if (scrollingPaused.value === true) {
        if (messages.value.at(-1) instanceof SkippedLogsEntry) {
          const lastEvent = messages.value.at(-1) as SkippedLogsEntry;
          const lastItem = buffer.value.at(-1) as LogEntry<string | JSONObject>;
          lastEvent.addSkippedEntries(buffer.value.length, lastItem);
        } else {
          const firstItem = buffer.value.at(0) as LogEntry<string | JSONObject>;
          const lastItem = buffer.value.at(-1) as LogEntry<string | JSONObject>;
          messages.value = [
            ...messages.value,
            new SkippedLogsEntry(new Date(), buffer.value.length, firstItem, lastItem, loadSkippedLogs),
          ];
        }
        buffer.value = [];
      } else {
        if (liveIncoming.length > config.maxLogs / 2) {
          messages.value = mergeChronologically([], liveIncoming).slice(-config.maxLogs / 2);
        } else {
          messages.value = mergeChronologically(messages.value, liveIncoming).slice(-config.maxLogs);
        }
        buffer.value = [];
        // Trimming the live window drops the oldest entries, including the
        // load-more sentinel at the top. Without re-adding it, once the live
        // window fills (fast on busy/host views) there is no way to scroll back
        // into history. Re-add it for history-capable streams.
        if (container || containers.value.length > 0) {
          ensureLoadMoreAtTop();
        }
      }
    } else {
      if (initial) {
        // sort the buffer the very first time because of multiple logs in parallel
        buffer.value.sort((a, b) => a.date.getTime() - b.date.getTime());

        if (container || containers.value.length > 0) {
          ensureLoadMoreAtTop();
        }
        initial = false;
      }
      messages.value = mergeChronologically(messages.value, liveIncoming);
      buffer.value = [];
    }
  }
  const flushBuffer = debounce(flushNow, 250, { maxWait: 1000 });
  let es: EventSource | null = null;
  // Background filtered stream used during a search snapshot to count how many new
  // matching logs arrive (without rendering them), so the UI can offer to go live.
  let counterEs: EventSource | null = null;
  let receivedInitialBackfill = false;

  function close() {
    if (es) {
      es.close();
      es = null;
    }
  }

  function closeCounter() {
    if (counterEs) {
      counterEs.close();
      counterEs = null;
    }
  }

  function connectSearchCounter() {
    closeCounter();
    if (!urlWithParams.value) return;
    pendingSearchCount.value = 0;
    counterEs = new EventSource(urlWithParams.value);
    // Only count live matches (onmessage). Backfill/container events are ignored.
    counterEs.onmessage = (e) => {
      if (e.data) pendingSearchCount.value += 1;
    };
  }

  function clearMessages() {
    flushBuffer.cancel();
    messages.value = [];
    buffer.value = [];
  }

  const urlWithParams = computed(() => {
    if (!url.value) {
      return undefined;
    }
    return withBase(`${url.value}?${params.value.toString()}`);
  });

  function connect({ clear } = { clear: true }) {
    closeCounter();
    if (isSearching.value && !followingSearch.value) {
      // Static snapshot of results, plus a background stream that counts new
      // matches so the UI can surface a "follow live" button.
      void loadSearch();
      connectSearchCounter();
      return;
    }
    if (!urlWithParams.value) {
      close();
      clearMessages();
      opened.value = false;
      loading.value = false;
      error.value = false;
      return;
    }
    close();
    if (clear) clearMessages();
    cached.value = false;
    cacheMode.value = "live";
    opened.value = false;
    loading.value = true;
    error.value = false;
    initial = true;
    searchStatus.value = { active: isSearching.value, done: false, matches: 0 };
    es = new EventSource(urlWithParams.value);
    es.addEventListener("container-event", (e) => {
      const event = parseEventData<{
        actorId: string;
        name: "container-stopped" | "container-started";
        time: string;
      }>(e);
      const containerEvent = new ContainerEventLogEntry(
        event.name == "container-started" ? "Container started" : "Container stopped",
        event.actorId,
        new Date(event.time),
        event.name,
      );

      buffer.value = [...buffer.value, containerEvent];
      flushBuffer();
      flushBuffer.flush();
    });

    es.addEventListener("logs-backfill", (e) => {
      const data = parseEventData<LogEvent[]>(e);
      let logs = data.map((e) => asLogEntry(e));
      const existingLogs = messages.value.filter((message) => !(message instanceof LoadMoreLogEntry));
      if (receivedInitialBackfill && existingLogs.length > 0) {
        const latestTimestamp = existingLogs.at(-1)!.date.getTime();
        // On SSE reconnect the server can replay an older cache block. Re-adding
        // rows that the bounded live window just discarded makes the DOM
        // oscillate between two sizes and visibly flashes several minutes back.
        // Keep only genuinely missed events; history remains available via the
        // load-more sentinel.
        logs = logs.filter((log) => log.date.getTime() > latestTimestamp);
      }
      receivedInitialBackfill = true;
      if (logs.length === 0) return;
      cached.value = true;
      cacheMode.value = "mixed";
      prependOlderLogs(logs);
    });

    es.addEventListener("search-status", (e) => {
      const data = parseEventData<{
        scannedTo: string;
        matches: number;
        done: boolean;
        reason?: "capped" | "exhausted";
      }>(e);
      searchStatus.value = {
        active: !data.done,
        done: data.done,
        matches: data.matches,
        scannedTo: data.scannedTo,
        reason: data.reason,
      };
    });

    es.onmessage = (e) => {
      if (e.data) {
        cacheMode.value = cached.value ? "mixed" : "live";
        buffer.value = [...buffer.value, parseMessage(e.data)];
        flushBuffer();
      }
    };
    es.onerror = () => {
      const state = es?.readyState;
      if (state === EventSource.CONNECTING) {
        // Keep UI in loading state while browser performs SSE auto-reconnect.
        loading.value = true;
        error.value = false;
        opened.value = false;
        return;
      }
      error.value = true;
      loading.value = false;
      opened.value = false;
    };
    es.onopen = () => {
      loading.value = false;
      opened.value = true;
      error.value = false;
    };
  }

  function ensureLoadMoreAtTop() {
    if (messages.value[0] instanceof LoadMoreLogEntry) return;
    messages.value = [new LoadMoreLogEntry(new Date(), loadOlderLogs), ...messages.value];
  }

  function prependOlderLogs(logs: LogEntry<LogMessage>[]) {
    if (logs.length === 0) return;
    if (container || containers.value.length > 0) {
      ensureLoadMoreAtTop();
    }
    const loader = messages.value[0] instanceof LoadMoreLogEntry ? messages.value[0] : undefined;
    const rest = loader ? messages.value.slice(1) : messages.value;
    const byKey = new Map<string, LogEntry<LogMessage>>();
    for (const log of [...logs, ...rest]) {
      const key = `${log.containerID ?? ""}:${log.id}`;
      if (!byKey.has(key)) {
        byKey.set(key, log);
      }
    }
    const merged = [...byKey.values()].sort((a, b) => a.date.getTime() - b.date.getTime()).slice(-config.maxLogs);
    messages.value = loader ? [loader, ...merged] : merged;
  }

  let searchToken = 0;
  async function loadSearch() {
    const token = ++searchToken;
    close();
    clearMessages();
    cached.value = true;
    cacheMode.value = "cache";
    opened.value = false;
    loading.value = true;
    error.value = false;
    initial = true;
    try {
      await loadSearchResults();
      if (token !== searchToken) return;
      opened.value = true;
      loading.value = false;
    } catch (err) {
      if (token !== searchToken) return;
      console.error(err);
      error.value = true;
      loading.value = false;
    }
  }

  const searchContainersKey = computed(() => allContainers.value.map((c) => `${c.host}:${c.id}`).join(","));

  watch(urlWithParams, () => connect(), { immediate: true });
  watch(searchContainersKey, () => {
    if (isSearching.value) connect();
  });
  // Opting into (or out of) live-follow during a search reconnects in the right mode.
  watch(followingSearch, () => {
    if (isSearching.value) connect();
  });

  onScopeDispose(() => {
    close();
    closeCounter();
  });

  watch(messages, () => {
    if (messages.value.length > 1) {
      hasComplexLogs.value = messages.value.some((m) => m instanceof ComplexLogEntry);
    }
  });

  return {
    messages,
    opened,
    error,
    loading,
    searchStatus,
  };
}
