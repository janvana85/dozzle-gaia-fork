import { mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import ScrollableView from "@/components/ScrollableView.vue";

/**
 * @vitest-environment jsdom
 */
describe("<ScrollableView />", () => {
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
    Element.prototype.scrollIntoView = vi.fn();
    vi.useFakeTimers();
    vi.setSystemTime(1_700_000_000_000);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  function mountView() {
    return mount(ScrollableView, {
      props: {
        scrollable: true,
      },
      global: {
        stubs: {
          ScrollProgress: true,
          "mdi:chevron-double-down": true,
        },
      },
      slots: {
        default: '<div style="height: 2000px">logs</div>',
      },
    });
  }

  test("pauses live follow when the user scrolls up away from the bottom", async () => {
    const wrapper = mountView();
    const scrollRoot = wrapper.find("main").element as HTMLElement;

    Object.defineProperty(scrollRoot, "scrollHeight", { configurable: true, value: 2_000 });
    Object.defineProperty(scrollRoot, "clientHeight", { configurable: true, value: 500 });

    scrollRoot.scrollTop = 1_500;
    await wrapper.find("main").trigger("scroll");

    expect(wrapper.text()).toContain("following live");

    scrollRoot.scrollTop = 900;
    await wrapper.find("main").trigger("scroll");

    expect(wrapper.text()).toContain("paused at history");
  });

  test("follow button resumes live follow explicitly", async () => {
    const wrapper = mountView();
    const scrollRoot = wrapper.find("main").element as HTMLElement;

    Object.defineProperty(scrollRoot, "scrollHeight", { configurable: true, value: 2_000 });
    Object.defineProperty(scrollRoot, "clientHeight", { configurable: true, value: 500 });

    scrollRoot.scrollTop = 1_500;
    await wrapper.find("main").trigger("scroll");
    scrollRoot.scrollTop = 900;
    await wrapper.find("main").trigger("scroll");
    await wrapper.find("button.btn-primary").trigger("click");

    expect(wrapper.text()).toContain("following live");
  });

  test("pauses live follow when the document scrolls up", async () => {
    const wrapper = mountView();
    const scrollRoot = wrapper.find("main").element as HTMLElement;
    const documentRoot = document.documentElement;

    Object.defineProperty(scrollRoot, "scrollHeight", { configurable: true, value: 500 });
    Object.defineProperty(scrollRoot, "clientHeight", { configurable: true, value: 500 });
    Object.defineProperty(document, "scrollingElement", { configurable: true, value: documentRoot });
    Object.defineProperty(documentRoot, "scrollHeight", { configurable: true, value: 2_000 });
    Object.defineProperty(documentRoot, "clientHeight", { configurable: true, value: 500 });

    documentRoot.scrollTop = 1_500;
    window.dispatchEvent(new Event("scroll"));
    await nextTick();

    expect(wrapper.text()).toContain("following live");

    documentRoot.scrollTop = 900;
    window.dispatchEvent(new Event("scroll"));
    await nextTick();

    expect(wrapper.text()).toContain("paused at history");
  });
});
