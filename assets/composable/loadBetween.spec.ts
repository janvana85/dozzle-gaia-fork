/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, test, vi } from "vitest";
import { ref } from "vue";
import { loadBetween } from "@/composable/loadBetween";
import { Container } from "@/models/Container";

vi.mock("@/stores/config", () => ({
  __esModule: true,
  default: { base: "" },
  withBase: (path: string) => path,
}));

function container(id: string): Container {
  return new Container(id, new Date(0), new Date(0), new Date(0), "img", id, "cmd", "host", {}, "running", 0, 0, []);
}

describe("loadBetween", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("throws the HTTP status instead of trying to parse an error body as JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("Bad Request", { status: 400, statusText: "Bad Request" })),
    );

    await expect(
      loadBetween(
        container("abc"),
        ref(new URLSearchParams()),
        new Date("2026-06-04T11:00:00Z"),
        new Date("2026-06-04T12:30:00Z"),
      ),
    ).rejects.toThrow(/loadBetween failed: 400 Bad Request/);
  });
});
