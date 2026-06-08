import { describe, expect, test } from "vitest";
import { normalizeVisibleKeyPath, normalizeVisibleKeysMap } from "./visibleKeys";

describe("normalizeVisibleKeyPath", () => {
  test("keeps string arrays unchanged", () => {
    expect(normalizeVisibleKeyPath(["a", "b"])).toEqual(["a", "b"]);
  });

  test("migrates dotted strings to arrays", () => {
    expect(normalizeVisibleKeyPath("a.b")).toEqual(["a", "b"]);
  });

  test("migrates object-like arrays to arrays", () => {
    expect(normalizeVisibleKeyPath({ 0: "a", 1: "b" })).toEqual(["a", "b"]);
  });
});

describe("normalizeVisibleKeysMap", () => {
  test("normalizes legacy key shapes inside a map", () => {
    const normalized = normalizeVisibleKeysMap(
      new Map<unknown, unknown>([
        ["a.b", true],
        [{ 0: "c", 1: "d" }, false],
      ]),
    );

    expect(Array.from(normalized.entries())).toEqual([
      [["a", "b"], true],
      [["c", "d"], false],
    ]);
  });
});
