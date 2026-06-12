/**
 * @vitest-environment jsdom
 */
import { mount } from "@vue/test-utils";
import { createTestingPinia } from "@pinia/testing";
import { reactive } from "vue";
import { beforeEach, describe, expect, test, vi } from "vitest";
import LogItem from "./LogItem.vue";
import { loggingContextKey } from "@/composable/logContext";
import { SimpleLogEntry, type Level } from "@/models/LogEntry";

function entry() {
  return new SimpleLogEntry(
    "message",
    "missing-container",
    1,
    new Date("2026-06-08T11:20:18.000Z"),
    "info",
    "stdout",
    "message",
  );
}

describe("<LogItem />", () => {
  beforeEach(() => {
    global.EventSource = class EventSource {
      close = vi.fn();
      addEventListener = vi.fn();
      onopen = null;
    } as any;
  });

  test("renders fallback labels when the container record is missing", () => {
    const wrapper = mount(LogItem, {
      props: { logEntry: entry() },
      global: {
        plugins: [
          createTestingPinia({
            createSpy: vi.fn,
            stubActions: false,
            initialState: {
              container: { containers: [] },
            },
          }),
        ],
        provide: {
          [loggingContextKey as symbol]: reactive({
            containers: [],
            streamConfig: { stdout: true, stderr: true },
            loadingMore: false,
            hasComplexLogs: false,
            cached: false,
            cacheMode: "live",
            levels: new Set<Level>(["info"]),
            showContainerName: true,
            showHostname: true,
            historical: false,
          }),
        },
        stubs: {
          LogActions: true,
          LogStd: true,
          LogDate: true,
          RandomColorTag: {
            name: "RandomColorTag",
            template: "<span><slot />{{ value }}</span>",
            props: ["value"],
          },
        },
      },
    });

    expect(wrapper.exists()).toBe(true);
  });
});
