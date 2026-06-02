/**
 * @vitest-environment jsdom
 */
import { beforeEach, describe, expect, test } from "vitest";
import { nextTick } from "vue";
import { useSearchFilter } from "./search";

describe("useSearchFilter", () => {
  // State is a module-level singleton, so reset before each test.
  beforeEach(() => {
    useSearchFilter().resetSearch();
  });

  test("isValidQuery reflects regex validity", () => {
    const { searchQueryFilter, isValidQuery } = useSearchFilter();
    searchQueryFilter.value = "foo.*";
    expect(isValidQuery.value).toBe(true);

    searchQueryFilter.value = "[";
    expect(isValidQuery.value).toBe(false);
  });

  test("toggleInverse flips the inverse flag", () => {
    const { inverseFilter, toggleInverse } = useSearchFilter();
    expect(inverseFilter.value).toBe(false);
    toggleInverse();
    expect(inverseFilter.value).toBe(true);
    toggleInverse();
    expect(inverseFilter.value).toBe(false);
  });

  test("resetSearch clears query, visibility and inverse", () => {
    const { searchQueryFilter, showSearch, inverseFilter, toggleInverse, resetSearch } = useSearchFilter();
    searchQueryFilter.value = "abc";
    showSearch.value = true;
    toggleInverse();

    resetSearch();

    expect(searchQueryFilter.value).toBe("");
    expect(showSearch.value).toBe(false);
    expect(inverseFilter.value).toBe(false);
  });

  test("startFollowingSearch enables follow and clears pending count", () => {
    const { followingSearch, pendingSearchCount, startFollowingSearch } = useSearchFilter();
    pendingSearchCount.value = 5;
    expect(followingSearch.value).toBe(false);

    startFollowingSearch();

    expect(followingSearch.value).toBe(true);
    expect(pendingSearchCount.value).toBe(0);
  });

  test("resetSearch clears follow state and pending count", () => {
    const { followingSearch, pendingSearchCount, startFollowingSearch, resetSearch } = useSearchFilter();
    pendingSearchCount.value = 3;
    startFollowingSearch();

    resetSearch();

    expect(followingSearch.value).toBe(false);
    expect(pendingSearchCount.value).toBe(0);
  });

  test("toggling inverse cancels an active follow and resets the count", async () => {
    const { followingSearch, pendingSearchCount, startFollowingSearch, toggleInverse } = useSearchFilter();
    startFollowingSearch();
    pendingSearchCount.value = 4;

    toggleInverse();
    await nextTick();

    expect(followingSearch.value).toBe(false);
    expect(pendingSearchCount.value).toBe(0);
  });
});
