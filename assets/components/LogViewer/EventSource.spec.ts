import { createTestingPinia } from "@pinia/testing";
import { mount } from "@vue/test-utils";
import { useSearchFilter } from "@/composable/search";
import { settings } from "@/stores/settings";
// @ts-ignore
import EventSource, { sources } from "eventsourcemock";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { computed, nextTick } from "vue";
import { createI18n } from "vue-i18n";
import { createRouter, createWebHistory } from "vue-router";
import { default as Component } from "./EventSource.vue";
import LogViewer from "@/components/LogViewer/LogViewer.vue";
import { Container } from "@/models/Container";
import { CacheGapLogEntry, Level, LoadMoreLogEntry } from "@/models/LogEntry";

vi.mock("@/stores/config", () => ({
  __esModule: true,
  default: { base: "", hosts: [{ name: "localhost", id: "localhost" }] },
  withBase: (path: string) => path,
}));

/**
 * @vitest-environment jsdom
 */
describe("<ContainerEventSource />", () => {
  const search = useSearchFilter();

  beforeEach(() => {
    global.EventSource = EventSource;
    // @ts-ignore
    window.scrollTo = vi.fn();
    global.IntersectionObserver = class IntersectionObserver {
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as any;
    vi.useFakeTimers();
    vi.setSystemTime(1560336942459);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  function createLogEventSource(
    {
      searchFilter = "",
      hourStyle = "auto",
    }: { searchFilter?: string | undefined; hourStyle?: "auto" | "24" | "12" } = {
      hourStyle: "auto",
    },
  ) {
    settings.value.hourStyle = hourStyle;
    search.searchQueryFilter.value = searchFilter;
    if (searchFilter) {
      search.showSearch.value = true;
    }

    const router = createRouter({
      history: createWebHistory("/"),
      routes: [
        {
          path: "/",
          component: {
            template: "Test from createLogEventSource",
          },
        },
        {
          name: "/container/[id].time.[datetime]",
          path: "/container/:id/time/:datetime",
          component: {
            template: "Test from createLogEventSource",
          },
        },
      ],
    });

    return mount(Component, {
      global: {
        plugins: [
          router,
          createTestingPinia({
            createSpy: vi.fn,
            stubActions: false,
            initialState: {
              container: { containers: [{ id: "abc", image: "test:v123", host: "localhost" }] },
            },
          }),
          createI18n({}),
        ],
        components: {
          LogViewer,
        },
        provide: {
          [scrollContextKey as symbol]: {
            paused: computed(() => false),
            loading: computed(() => false),
          },
          [loggingContextKey as symbol]: {
            containers: computed(() => [{ id: "abc", image: "test:v123", host: "localhost" }]),
            streamConfig: reactive({ stdout: true, stderr: true }),
            hasComplexLogs: ref(false),
            levels: new Set<Level>(["info"]),
            historical: ref(false),
          },
        },
      },
      slots: {
        default: `
        <template #scoped="params"><LogViewer :messages="params.messages" :show-container-name="false" :visible-keys="[]" /></template>
        `,
      },
      props: {
        streamSource: useContainerStream,
        entity: new Container(
          "abc",
          new Date(), // created
          new Date(), // started
          new Date(), // finished
          "image",
          "name",
          "command",
          "localhost",
          {},
          "created",
          0,
          0,
          [],
        ),
      },
    });
  }

  function createMergedLogEventSource() {
    const router = createRouter({
      history: createWebHistory("/"),
      routes: [{ path: "/", component: { template: "Test from createMergedLogEventSource" } }],
    });

    const containers = [
      new Container(
        "abc",
        new Date(),
        new Date(),
        new Date(),
        "image",
        "name-a",
        "command",
        "localhost",
        {},
        "created",
        0,
        0,
        [],
      ),
      new Container(
        "def",
        new Date(),
        new Date(),
        new Date(),
        "image",
        "name-b",
        "command",
        "localhost",
        {},
        "created",
        0,
        0,
        [],
      ),
    ];

    return mount(Component, {
      global: {
        plugins: [
          router,
          createTestingPinia({
            createSpy: vi.fn,
            stubActions: false,
            initialState: {
              container: { containers: containers.map((c) => ({ id: c.id, image: c.image, host: c.host })) },
            },
          }),
          createI18n({}),
        ],
        components: {
          LogViewer,
        },
        provide: {
          [scrollContextKey as symbol]: reactive({
            paused: false,
            progress: 1,
            currentDate: new Date(),
            hasProgress: false,
          }),
          [loggingContextKey as symbol]: reactive({
            containers,
            streamConfig: { stdout: true, stderr: true },
            hasComplexLogs: false,
            levels: new Set<Level>(["info"]),
            historical: false,
            loadingMore: false,
          }),
        },
      },
      slots: {
        default: `
        <template #scoped="params"><LogViewer :messages="params.messages" :show-container-name="false" :visible-keys="[]" /></template>
        `,
      },
      props: {
        streamSource: useMergedStream,
        entity: containers,
      },
    });
  }

  const sourceUrl = "/api/hosts/localhost/containers/abc/logs/stream?stdout=1&stderr=1&levels=info";
  const mergedSourceUrl = "/api/hosts/localhost/logs/mergedStream/abc,def?stdout=1&stderr=1&levels=info";

  test("renders loading correctly", async () => {
    const wrapper = createLogEventSource();
    expect(wrapper.find("ul.animate-pulse").exists()).toBe(true);
  });

  test("should connect to EventSource", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    expect(sources[sourceUrl].readyState).toBe(1);
    wrapper.unmount();
  });

  test("should close EventSource", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    wrapper.unmount();
    expect(sources[sourceUrl].readyState).toBe(2);
  });

  test("should parse messages", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    sources[sourceUrl].emitMessage({
      data: `{"ts":1560336942459, "m":"This is a message.", "id":1, "rm": "This is a message.", "c": "abc"}`,
    });

    vi.runAllTimers();
    await nextTick();

    // @ts-ignore
    const [message, _] = wrapper.vm.messages;
    expect(message).toMatchSnapshot();
  });

  test("keeps load-more sentinel above cache backfill", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    sources[sourceUrl].emitMessage({
      data: `{"ts":1560336942459, "m":"Live message.", "id":2, "rm": "Live message.", "c": "abc"}`,
    });

    vi.runAllTimers();
    await nextTick();

    sources[sourceUrl].emit("logs-backfill", {
      data: `[{"ts":1560336882459, "m":"Cached message.", "id":1, "rm": "Cached message.", "c": "abc"}]`,
    });
    await nextTick();

    // @ts-ignore
    expect(wrapper.vm.messages[0]).toBeInstanceOf(LoadMoreLogEntry);
    // @ts-ignore
    expect(wrapper.vm.messages[1].message).toBe("Cached message.");
    // @ts-ignore
    expect(wrapper.vm.messages[2].message).toBe("Live message.");
  });

  test("does not lose early cache backfill before first live message", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    sources[sourceUrl].emit("logs-backfill", {
      data: `[{"ts":1560336882459, "m":"Cached message.", "id":1, "rm": "Cached message.", "c": "abc"}]`,
    });
    await nextTick();

    sources[sourceUrl].emitMessage({
      data: `{"ts":1560336942459, "m":"Live message.", "id":2, "rm": "Live message.", "c": "abc"}`,
    });
    vi.runAllTimers();
    await nextTick();

    // @ts-ignore
    expect(wrapper.vm.messages[0]).toBeInstanceOf(LoadMoreLogEntry);
    // @ts-ignore
    expect(wrapper.vm.messages[1].message).toBe("Cached message.");
    // @ts-ignore
    expect(wrapper.vm.messages[2].message).toBe("Live message.");
  });

  test("parses cache-gap next chunk hints from backfill events", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    sources[sourceUrl].emit("logs-backfill", {
      data: `[{"t":"cache-gap","m":"not found in cache, fetching from docker logs","ts":1560336882459,"id":-1,"l":"info","s":"stderr","c":"abc","rm":"not found in cache, fetching from docker logs","from":"2026-06-03T10:00:00Z","to":"2026-06-03T11:00:00Z","nextFrom":"2026-06-01T10:00:00Z","nextTo":"2026-06-01T10:01:00Z"}]`,
    });
    await nextTick();

    // @ts-ignore
    const gap = wrapper.vm.messages[1];
    expect(gap).toBeInstanceOf(CacheGapLogEntry);
    expect(gap.nextFrom?.toISOString()).toBe("2026-06-01T10:00:00.000Z");
    expect(gap.nextTo?.toISOString()).toBe("2026-06-01T10:01:00.000Z");
  });

  test("keeps merged live messages globally ordered when a slower container arrives late", async () => {
    const wrapper = createLogEventSource();
    sources[sourceUrl].emitOpen();
    sources[sourceUrl].emitMessage({
      data: `{"ts":1560336943011, "m":"newer", "id":2, "rm":"newer", "c":"abc"}`,
    });
    sources[sourceUrl].emitMessage({
      data: `{"ts":1560336940998, "m":"older", "id":3, "rm":"older", "c":"def"}`,
    });

    vi.runAllTimers();
    await nextTick();

    // @ts-ignore
    const texts = wrapper.vm.messages.slice(1).map((message) => message.message);
    expect(texts).toEqual(["older", "newer"]);
  });

  test("drops very late merged live messages while following the live tail", async () => {
    const wrapper = createMergedLogEventSource();
    sources[mergedSourceUrl].emitOpen();
    sources[mergedSourceUrl].emitMessage({
      data: `{"ts":1560336945000, "m":"live", "id":10, "rm":"live", "c":"abc"}`,
    });
    vi.runAllTimers();
    await nextTick();

    sources[mergedSourceUrl].emitMessage({
      data: `{"ts":1560336881000, "m":"stale", "id":11, "rm":"stale", "c":"def"}`,
    });
    vi.runAllTimers();
    await nextTick();

    // @ts-ignore
    const texts = wrapper.vm.messages.slice(1).map((message) => message.message);
    expect(texts).toEqual(["live"]);
  });

  describe("render html correctly", () => {
    test("should render messages", async () => {
      const wrapper = createLogEventSource();
      sources[sourceUrl].emitOpen();
      sources[sourceUrl].emitMessage({
        data: `{"ts":1560336942459, "m":"This is a message.", "id":1, "rm": "This is a message.", "c": "abc"}`,
      });

      vi.runAllTimers();
      await nextTick();

      expect(wrapper.find("ul[data-logs]").html()).toMatchSnapshot();
    });

    test("should render dates with 12 hour style", async () => {
      const wrapper = createLogEventSource({ hourStyle: "12" });
      sources[sourceUrl].emitOpen();
      sources[sourceUrl].emitMessage({
        data: `{"ts":1560336942459, "m":"foo bar", "id":1, "rm": "foo bar", "c": "abc"}`,
      });

      vi.runAllTimers();
      await nextTick();

      expect(wrapper.find("ul[data-logs]").html()).toMatchSnapshot();
    });

    test("should render dates with 24 hour style", async () => {
      const wrapper = createLogEventSource({ hourStyle: "24" });
      sources[sourceUrl].emitOpen();
      sources[sourceUrl].emitMessage({
        data: `{"ts":1560336942459, "m":"foo bar", "id":1, "c": "abc"}`,
      });

      vi.runAllTimers();
      await nextTick();

      expect(wrapper.find("ul[data-logs]").html()).toMatchSnapshot();
    });
  });
});
