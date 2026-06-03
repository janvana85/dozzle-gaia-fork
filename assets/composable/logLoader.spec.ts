/**
 * @vitest-environment jsdom
 */
import { mount } from "@vue/test-utils";
import { afterEach, describe, expect, test, vi } from "vitest";
import { defineComponent, provide, ref, shallowRef, type Ref, type ShallowRef } from "vue";
import { loggingContextKey } from "@/composable/logContext";
import { useLogLoader } from "@/composable/logLoader";
import { Container } from "@/models/Container";
import { CacheGapLogEntry, LoadMoreLogEntry, LogEntry, type LogMessage, SimpleLogEntry } from "@/models/LogEntry";

// Mock the per-container history fetch so the test is deterministic.
const loadBetween = vi.fn();
vi.mock("@/composable/loadBetween", () => ({
  loadBetween: (...args: unknown[]) => loadBetween(...args),
  loadCachedSearch: vi.fn(),
  parseMessage: vi.fn(),
}));

function entry(containerID: string, id: number, tsSeconds: number): SimpleLogEntry {
  return new SimpleLogEntry("msg", containerID, id, new Date(tsSeconds * 1000), "info", "stdout", "msg");
}

function gap(containerID: string, fromSeconds: number, toSeconds: number): CacheGapLogEntry {
  return new CacheGapLogEntry(
    "not found in cache, fetching from docker logs",
    containerID,
    -fromSeconds,
    new Date(fromSeconds * 1000),
    new Date(fromSeconds * 1000),
    new Date(toSeconds * 1000),
  );
}

function container(id: string): Container {
  return new Container(id, new Date(0), new Date(0), new Date(0), "img", id, "cmd", "host", {}, "running", 0, 0, []);
}

// Drives useLogLoader inside a component so injection works.
function setup(messages: ShallowRef<LogEntry<LogMessage>[]>, containers: Ref<Container[]>) {
  let api!: ReturnType<typeof useLogLoader>;
  const Comp = defineComponent({
    setup() {
      provide(loggingContextKey, { cached: ref(false), cacheMode: ref("live") } as never);
      api = useLogLoader(messages, containers, ref(new URLSearchParams()), ref(false));
      return () => null;
    },
  });
  mount(Comp);
  return api;
}

const ok = (logs: LogEntry<LogMessage>[]) => ({ logs, signal: { aborted: false } as AbortSignal });

describe("useLogLoader.loadOlderLogs (merged history)", () => {
  afterEach(() => {
    loadBetween.mockReset();
  });

  test("merges older logs from containers at different depths into one sorted, hole-free list", async () => {
    const loader = new LoadMoreLogEntry(new Date(), async () => {});
    const messages = shallowRef<LogEntry<LogMessage>[]>([
      loader,
      entry("a", 100, 100),
      entry("b", 100, 150),
      entry("a", 101, 200),
      entry("b", 101, 250),
    ]);
    const containers = shallowRef([container("a"), container("b")]);
    const { loadOlderLogs } = setup(messages, containers);

    loadBetween.mockImplementation((c: Container) =>
      Promise.resolve(
        c.id === "a" ? ok([entry("a", 98, 50), entry("a", 99, 75)]) : ok([entry("b", 98, 60), entry("b", 99, 90)]),
      ),
    );

    await loadOlderLogs(loader);

    const rest = messages.value.slice(1); // drop loader
    // No holes
    expect(rest.every((m) => m != null)).toBe(true);
    // Globally sorted ascending by date
    const times = rest.map((m) => m.date.getTime());
    expect(times).toEqual([...times].sort((x, y) => x - y));
    // Oldest advanced to the newly loaded 50s entry
    expect(rest[0].date.getTime()).toBe(50 * 1000);
    // Loader still at the top
    expect(messages.value[0]).toBeInstanceOf(LoadMoreLogEntry);
  });

  test("drops undefined holes and de-duplicates overlapping fetches", async () => {
    const loader = new LoadMoreLogEntry(new Date(), async () => {});
    const existing = entry("a", 100, 100);
    const messages = shallowRef<LogEntry<LogMessage>[]>([loader, existing]);
    const containers = shallowRef([container("a")]);
    const { loadOlderLogs } = setup(messages, containers);

    // Returns an older log, a hole (undefined), and a duplicate of the existing entry.
    loadBetween.mockResolvedValue(ok([entry("a", 99, 75), undefined as never, entry("a", 100, 100)]));

    await loadOlderLogs(loader);

    const rest = messages.value.slice(1);
    expect(rest.every((m) => m != null)).toBe(true); // hole removed
    const ids = rest.map((m) => `${m.containerID}:${m.id}`);
    expect(new Set(ids).size).toBe(ids.length); // no duplicates
    expect(rest.map((m) => m.date.getTime())).toEqual([75 * 1000, 100 * 1000]); // sorted, deduped
  });

  test("does not re-enter while a previous load is in flight", async () => {
    const loader = new LoadMoreLogEntry(new Date(), async () => {});
    const messages = shallowRef<LogEntry<LogMessage>[]>([loader, entry("a", 100, 100)]);
    const containers = shallowRef([container("a")]);
    const loadingMore = ref(true); // simulate an in-flight load
    let api!: ReturnType<typeof useLogLoader>;
    const Comp = defineComponent({
      setup() {
        provide(loggingContextKey, { cached: ref(false), cacheMode: ref("live") } as never);
        api = useLogLoader(messages, containers, ref(new URLSearchParams()), loadingMore);
        return () => null;
      },
    });
    mount(Comp);

    await api.loadOlderLogs(loader);

    expect(loadBetween).not.toHaveBeenCalled();
  });

  test("removes a cache gap placeholder when real logs arrive for the gap", async () => {
    const loader = new LoadMoreLogEntry(new Date(), async () => {});
    const existingGap = gap("a", 50, 100);
    const messages = shallowRef<LogEntry<LogMessage>[]>([loader, existingGap, entry("a", 101, 120)]);
    const containers = shallowRef([container("a")]);
    const { loadOlderLogs } = setup(messages, containers);

    loadBetween.mockResolvedValue(ok([entry("a", 99, 75)]));

    await loadOlderLogs(loader);

    expect(messages.value.some((log) => log instanceof CacheGapLogEntry)).toBe(false);
    expect(messages.value.map((log) => log.id)).toContain(99);
  });

  test("keeps a cache gap placeholder when the only real log is on the gap boundary", async () => {
    const loader = new LoadMoreLogEntry(new Date(), async () => {});
    const existing = entry("a", 100, 100);
    const messages = shallowRef<LogEntry<LogMessage>[]>([loader, existing]);
    const containers = shallowRef([container("a")]);
    const { loadOlderLogs } = setup(messages, containers);

    loadBetween.mockResolvedValue(ok([gap("a", 50, 100), entry("a", 100, 100)]));

    await loadOlderLogs(loader);

    expect(messages.value[1]).toBeInstanceOf(CacheGapLogEntry);
    expect(messages.value.map((log) => log.id)).toEqual([loader.id, -50, 100]);
  });

  test("uses cache-gap next chunk hints to jump directly across large retained gaps", async () => {
    const loader = new LoadMoreLogEntry(new Date(), async () => {});
    const jumpGap = new CacheGapLogEntry(
      "not found in cache, fetching from docker logs",
      "a",
      -20,
      new Date(20 * 1000),
      new Date(20 * 1000),
      new Date(100 * 1000),
      new Date(10 * 1000),
      new Date(20 * 1000),
    );
    const messages = shallowRef<LogEntry<LogMessage>[]>([loader, jumpGap, entry("a", 100, 100)]);
    const containers = shallowRef([container("a")]);
    const { loadOlderLogs } = setup(messages, containers);

    loadBetween.mockResolvedValue(ok([entry("a", 10, 10), entry("a", 11, 20)]));

    await loadOlderLogs(loader);

    expect(loadBetween).toHaveBeenCalledWith(
      expect.objectContaining({ id: "a" }),
      expect.anything(),
      new Date(10 * 1000),
      new Date(20 * 1000),
      { min: 100 },
    );
    expect(messages.value.slice(1).map((log) => log.id)).toEqual([10, 11, 100]);
  });
});
