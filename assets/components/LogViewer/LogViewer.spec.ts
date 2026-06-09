/**
 * @vitest-environment jsdom
 */
import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";

import LogViewer from "./LogViewer.vue";
import { SimpleLogEntry } from "@/models/LogEntry";

function entries(count: number) {
  return Array.from(
    { length: count },
    (_, index) =>
      new SimpleLogEntry(`log ${index}`, "container", index, new Date(index), "info", "stdout", `log ${index}`),
  );
}

describe("LogViewer", () => {
  it("does not reset a fully rendered live window to the initial batch", async () => {
    const idleCallbacks: FrameRequestCallback[] = [];
    vi.stubGlobal("requestIdleCallback", (callback: FrameRequestCallback) => {
      idleCallbacks.push(callback);
      return idleCallbacks.length;
    });
    vi.stubGlobal("cancelIdleCallback", vi.fn());

    const wrapper = mount(LogViewer, {
      props: {
        messages: entries(400),
        visibleKeys: new Map(),
      },
      global: {
        stubs: {
          LogList: {
            props: ["messages"],
            template: '<div data-testid="count">{{ messages.length }}</div>',
          },
        },
      },
    });

    expect(wrapper.get('[data-testid="count"]').text()).toBe("300");
    idleCallbacks.shift()?.(0);
    await nextTick();
    expect(wrapper.get('[data-testid="count"]').text()).toBe("400");

    await wrapper.setProps({ messages: entries(401) });
    await flushPromises();

    expect(wrapper.get('[data-testid="count"]').text()).toBe("401");
    expect(idleCallbacks).toHaveLength(0);
    vi.unstubAllGlobals();
  });
});
