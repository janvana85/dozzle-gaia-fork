/**
 * @vitest-environment jsdom
 */
import { mount } from "@vue/test-utils";
import { afterEach, describe, expect, test, vi } from "vitest";
import Popup from "./Popup.vue";
import { popupDelayMs } from "@/composable/popup";

describe("<Popup />", () => {
  afterEach(() => {
    vi.useRealTimers();
    document.body.innerHTML = "";
  });

  test("shows quickly on trigger hover without intercepting pointer events", async () => {
    vi.useFakeTimers();
    const wrapper = mount(Popup, {
      attachTo: document.body,
      slots: {
        default: '<a href="#">container</a>',
        content: "STATE RUNNING",
      },
    });

    await wrapper.find("span").trigger("mouseenter");
    expect((document.body.querySelector("div.fixed") as HTMLElement).style.display).toBe("none");

    await vi.advanceTimersByTimeAsync(popupDelayMs);
    await nextTick();

    const popup = document.body.querySelector("div.fixed") as HTMLElement;
    expect(popup).not.toBeNull();
    expect(popup.style.display).not.toBe("none");
    expect(popup.classList.contains("pointer-events-none")).toBe(true);
  });

  test("does not reset hover state when pointer moves across nested trigger content", async () => {
    vi.useFakeTimers();
    const wrapper = mount(Popup, {
      attachTo: document.body,
      slots: {
        default: `
          <a href="#">
            <span class="status"></span>
            <span class="name">container</span>
          </a>
        `,
        content: "STATE RUNNING",
      },
    });

    await wrapper.find("span.contents").trigger("mouseenter");
    await vi.advanceTimersByTimeAsync(popupDelayMs - 1);
    await wrapper.find(".status").trigger("mouseout");
    await wrapper.find(".name").trigger("mouseover");
    await vi.advanceTimersByTimeAsync(1);
    await nextTick();

    const popup = document.body.querySelector("div.fixed") as HTMLElement;
    expect(popup.style.display).not.toBe("none");
  });
});
