const searchQueryFilter = ref<string>("");
const debouncedSearchFilter = refDebounced(searchQueryFilter);
const showSearch = ref(false);
const inverseFilter = ref(false);

// Live-follow state for search. While searching, the view shows a static snapshot
// of results. `pendingSearchCount` tracks how many new matching logs have streamed
// in since the snapshot (counted by a background filtered stream). `followingSearch`
// flips on when the user opts into tailing those new matches live.
const followingSearch = ref(false);
const pendingSearchCount = ref(0);

const searchParams = new URLSearchParams(window.location.search);
if (searchParams.get("search") !== null && searchParams.get("search") !== "") {
  searchQueryFilter.value = searchParams.get("search") || "";
  showSearch.value = true;
}
function resetSearch() {
  searchQueryFilter.value = "";
  showSearch.value = false;
  inverseFilter.value = false;
  followingSearch.value = false;
  pendingSearchCount.value = 0;
}

function toggleInverse() {
  inverseFilter.value = !inverseFilter.value;
}

// Opt into following new matching logs live. Clears the pending count; the stream
// reconnects as a live filtered stream by watching `followingSearch`.
function startFollowingSearch() {
  pendingSearchCount.value = 0;
  followingSearch.value = true;
}

// A changed query (or inverse toggle) means a new snapshot, so any prior follow
// state and pending count no longer apply.
watch([debouncedSearchFilter, inverseFilter], () => {
  followingSearch.value = false;
  pendingSearchCount.value = 0;
});

const isSearching = computed(() => showSearch.value && debouncedSearchFilter.value !== "");

const isValidQuery = computed(() => {
  try {
    new RegExp(searchQueryFilter.value);
    return true;
  } catch (e) {
    return false;
  }
});

export function useSearchFilter() {
  return {
    searchQueryFilter,
    isValidQuery,
    debouncedSearchFilter,
    showSearch,
    resetSearch,
    isSearching,
    inverseFilter,
    toggleInverse,
    followingSearch,
    pendingSearchCount,
    startFollowingSearch,
  };
}
