import { type Ref } from "vue";
import { type LogEvent, type LogMessage, LogEntry, asLogEntry } from "@/models/LogEntry";
import { Container } from "@/models/Container";

export function parseMessage(data: string): LogEntry<LogMessage> {
  const e = JSON.parse(data) as LogEvent;
  return asLogEntry(e);
}

export async function loadBetween(
  container: Container | Ref<Container>,
  params: Ref<URLSearchParams>,
  from: Date,
  to: Date,
  {
    lastSeenId,
    startId,
    min,
    maxStart,
  }: { lastSeenId?: number; startId?: number; min?: number; maxStart?: number } = {},
) {
  const c = toValue(container);
  const url = `/api/hosts/${c.host}/containers/${c.id}/logs`;
  const abortController = new AbortController();
  const signal = abortController.signal;

  function buildUrl() {
    const loadMoreParams = new URLSearchParams(params.value);
    loadMoreParams.append("from", from.toISOString());
    loadMoreParams.append("to", to.toISOString());
    if (min) {
      loadMoreParams.append("min", String(min));
    }
    if (maxStart) {
      loadMoreParams.append("maxStart", String(maxStart));
    }
    if (lastSeenId) {
      loadMoreParams.append("lastSeenId", String(lastSeenId));
    }
    if (startId) {
      loadMoreParams.append("startId", String(startId));
    }
    return withBase(`${url}?${loadMoreParams.toString()}`);
  }

  const fullUrl = buildUrl();
  const stopWatcher = watchOnce(params, () => abortController.abort("stream changed"));
  let logs: string;
  let response: Response;
  try {
    response = await fetch(fullUrl, { signal });
    logs = await response.text();
  } catch (e) {
    stopWatcher();
    // Aborting an in-flight request when the stream/view changes is expected;
    // resolve with an empty result (callers filter on signal.aborted) instead
    // of leaking an uncaught rejection to the console.
    if (signal.aborted) return { logs: [] as LogEntry<LogMessage>[], signal };
    throw e;
  }
  stopWatcher();

  if (!response.ok) {
    const snippet = logs.trim().slice(0, 200);
    throw new Error(
      `loadBetween failed: ${response.status} ${response.statusText} for ${fullUrl}${snippet ? `: ${snippet}` : ""}`,
    );
  }

  if (!logs) return { logs: [] as LogEntry<LogMessage>[], signal };

  const parsedLogs: LogEntry<LogMessage>[] = [];
  const lines = logs.trim().split("\n");
  const batchSize = 250;
  for (let i = 0; i < lines.length; i += batchSize) {
    const batch = lines.slice(i, i + batchSize);
    for (const line of batch) {
      if (line) {
        parsedLogs.push(parseMessage(line));
      }
    }
    if (i + batchSize < lines.length) {
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
    }
  }

  return {
    logs: parsedLogs,
    signal,
  };
}

export async function loadCachedSearch(
  container: Container | Ref<Container>,
  params: Ref<URLSearchParams>,
  before: Date,
  limit = 200,
) {
  const c = toValue(container);
  const searchParams = new URLSearchParams(params.value);
  searchParams.append("cachedSearch", "1");
  searchParams.append("limit", String(limit));
  searchParams.append("from", new Date(before.getTime() - 1).toISOString());
  searchParams.append("to", before.toISOString());

  const abortController = new AbortController();
  const signal = abortController.signal;
  const stopWatcher = watchOnce(params, () => abortController.abort("search changed"));
  let response: Response;
  let text: string;
  try {
    response = await fetch(withBase(`/api/hosts/${c.host}/containers/${c.id}/logs?${searchParams.toString()}`), {
      signal,
    });
    text = await response.text();
  } catch (e) {
    stopWatcher();
    // Aborting an in-flight request when the search/view changes is expected;
    // resolve with an empty result (callers filter on signal.aborted) instead
    // of leaking an uncaught rejection to the console.
    if (signal.aborted) return { logs: [] as LogEntry<LogMessage>[], hasMore: false, signal };
    throw e;
  }
  stopWatcher();
  if (!response.ok) throw new Error(`cached search failed: ${response.status}`);
  const logs = text
    ? text
        .trim()
        .split("\n")
        .filter(Boolean)
        .map((line) => parseMessage(line))
    : [];

  return {
    logs,
    hasMore: response.headers.get("X-Has-More") === "true",
    signal,
  };
}
