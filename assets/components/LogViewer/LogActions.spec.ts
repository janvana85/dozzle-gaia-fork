/**
 * @vitest-environment jsdom
 */
import { mount } from "@vue/test-utils";
import { ref } from "vue";
import { createI18n } from "vue-i18n";
import { describe, expect, test, vi } from "vitest";
import { createRouter, createWebHistory } from "vue-router";
import LogActions from "./LogActions.vue";
import { SimpleLogEntry } from "@/models/LogEntry";

const copy = vi.fn();

vi.mock("@vueuse/core", async () => {
  const actual = await vi.importActual<typeof import("@vueuse/core")>("@vueuse/core");
  return {
    ...actual,
    useClipboard: () => ({
      copy,
      isSupported: ref(true),
      copied: ref(false),
    }),
  };
});

function entry() {
  return new SimpleLogEntry(
    "message",
    "missing-container",
    7,
    new Date("2026-06-08T11:20:18.000Z"),
    "info",
    "stdout",
    "message",
  );
}

function createRouterForTest() {
  return createRouter({
    history: createWebHistory("/"),
    routes: [
      { path: "/", component: { template: "<div />" } },
      {
        name: "/container/[id].time.[datetime]",
        path: "/container/:id/time/:datetime",
        component: { template: "<div />" },
      },
    ],
  });
}

describe("<LogActions />", () => {
  test("copies a permalink using logEntry.containerID when the container record is missing", async () => {
    const router = createRouterForTest();
    const resolveSpy = vi.spyOn(router, "resolve");
    await router.push("/");
    await router.isReady();

    const wrapper = mount(LogActions, {
      props: { logEntry: entry(), container: undefined },
      global: {
        plugins: [router, createI18n({ legacy: false, locale: "en", messages: { en: {} } })],
        stubs: {
          "material-symbols:content-copy": true,
          "material-symbols:link": true,
          "material-symbols:code-blocks-rounded": true,
          "ion:ellipsis-vertical": true,
          "mdi:bell": true,
        },
      },
    });

    await wrapper.findAll("a")[1].trigger("click");

    expect(resolveSpy).toHaveBeenCalledWith({
      name: "/container/[id].time.[datetime]",
      params: { id: "missing-container", datetime: "2026-06-08T11:20:18.000Z" },
      query: { logId: 7 },
    });
    expect(copy).toHaveBeenCalledTimes(1);
  });
});
