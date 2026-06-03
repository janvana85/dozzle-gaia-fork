# Next Steps

## Cached search aggregation

Current historical scroll-back in merged views now skips large retained gaps via `log_chunks` hints and is fast enough for production live use.

The next real bottleneck is cached search in merged/group/host views:

- the frontend still fans out one cached-search request per container
- latency grows with container count even when the cache is warm
- this is the remaining path where manual `grep` can still feel competitive

Recommended follow-up:

1. Add backend aggregated cached-search endpoints for merged/group/host-group scopes.
2. Query the log store once per scope instead of once per container.
3. Return globally sorted results plus `hasMore` pagination metadata.
4. Move merged historical search to that backend path and keep the current per-container path only for single-container views.

Expected outcome:

- lower request fanout
- more predictable latency at ~40+ containers
- much faster historical search in production
