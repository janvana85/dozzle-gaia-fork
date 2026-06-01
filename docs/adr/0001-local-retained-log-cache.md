# Local retained log cache and server-side cached search

Dozzle Gaia intentionally diverges from upstream Dozzle's live-only log viewing by retaining recent container log history locally. The local **Log Cache** keeps at most the configured **Retention Window**, backfills only available history within that window, and must not block live log viewing while it catches up.

We chose server-side **Cached Log Search** over browser-side filtering of retained history. Search returns bounded, paged results scoped to the current log-viewing context by default, starting with the most recent matches and paging backward to older matches; live log matches become visible after refresh rather than mutating an open result set. This trades extra disk and backend work for fast search, stable scroll behavior, and avoiding CPU spikes or browser hangs when retained logs are large.
