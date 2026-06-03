/**
 * @vitest-environment jsdom
 */
import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { computed, reactive } from "vue";
import LogList from "@/components/LogViewer/LogList.vue";
import { loggingContextKey } from "@/composable/logContext";
import { scrollContextKey } from "@/composable/scrollContext";
import { Container } from "@/models/Container";
import { SimpleLogEntry, type Level } from "@/models/LogEntry";

function container(id: string) {
  return new Container(id, new Date(0), new Date(0), new Date(0), "img", id, "cmd", "host", {}, "running", 0, 0, []);
}

function entry(containerID: string, id: number, ts: number, message: string) {
  return new SimpleLogEntry(message, containerID, id, new Date(ts), "info", "stdout", message);
}

describe("<LogList />", () => {
  beforeEach(() => {
    global.IntersectionObserver = class IntersectionObserver {
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as any;
  });

  test("uses composite DOM ids when merged logs reuse the same per-container log id", () => {
    const wrapper = mount(LogList, {
      shallow: true,
      props: {
        messages: [entry("alpha", 42, 1_000, "alpha"), entry("beta", 42, 2_000, "beta")],
      },
      global: {
        provide: {
          [loggingContextKey as symbol]: reactive({
            containers: computed(() => [container("alpha"), container("beta")]),
            streamConfig: reactive({ stdout: true, stderr: true }),
            loadingMore: false,
            hasComplexLogs: false,
            cached: false,
            cacheMode: "live",
            levels: new Set<Level>(["info"]),
            showContainerName: false,
            showHostname: false,
            historical: false,
          }),
          [scrollContextKey as symbol]: reactive({
            paused: false,
            hasProgress: false,
            progress: 1,
            currentDate: new Date(0),
          }),
        },
      },
    });

    const ids = wrapper.findAll("li").map((node) => node.attributes("id"));
    expect(ids).toEqual(["alpha-42", "beta-42"]);
  });
});
