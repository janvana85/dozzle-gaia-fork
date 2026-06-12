/**
 * @vitest-environment jsdom
 */
import { mount } from "@vue/test-utils";
import { afterEach, describe, expect, test, vi } from "vitest";
import CacheGapLogItem from "./CacheGapLogItem.vue";
import { CacheGapLogEntry } from "@/models/LogEntry";

describe("<CacheGapLogItem />", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  test("auto-hides the cache-gap banner after a short timeout", async () => {
    vi.useFakeTimers();
    const wrapper = mount(CacheGapLogItem, {
      props: {
        logEntry: new CacheGapLogEntry(
          "trying to fetch from docker logs",
          "abc",
          -1,
          new Date("2026-06-08T00:00:00Z"),
          new Date("2026-06-08T00:00:00Z"),
          new Date("2026-06-08T00:05:00Z"),
        ),
      },
    });

    expect(wrapper.text()).toContain("trying to fetch from docker logs");

    vi.advanceTimersByTime(3000);
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toBe("");
  });
});
