import { Container } from "@/models/Container";
import { hasNormalizedVisibleKeys, normalizeVisibleKeysMap } from "@/utils";

const DOZZLE_HOST = "DOZZLE_HOST";
export const sessionHost = useSessionStorage<string | null>(DOZZLE_HOST, null);

if (config.hosts.length === 1 && !sessionHost.value) {
  sessionHost.value = config.hosts[0].id;
}

const storage = useProfileStorage("visibleKeys", new Map<string, Map<string[], boolean>>(), {
  from(transformed: [string, [string[], boolean][]][]) {
    return new Map(
      transformed.map(([key, value]) => [key, normalizeVisibleKeysMap(new Map(value as [unknown, unknown][]))]),
    );
  },
  to(value: Map<string, Map<string[], boolean>>) {
    const outer = Array.from(value.entries());
    const inner = outer.map(([key, value]) => [key, Array.from(value.entries())]);
    return inner;
  },
});
export function persistentVisibleKeysForContainer(container: Ref<Container | undefined>): Ref<Map<string[], boolean>> {
  const fallback = ref(new Map<string[], boolean>());

  // Computed property to only store to storage when the value changes
  return computed({
    get: () => {
      if (!container.value) {
        return fallback.value;
      }
      const stored = storage.value.get(container.value.storageKey);
      if (!stored) {
        return new Map<string[], boolean>();
      }

      if (hasNormalizedVisibleKeys(stored)) {
        return stored;
      }

      const normalized = normalizeVisibleKeysMap(stored);
      storage.value.set(container.value.storageKey, normalized);
      return normalized;
    },
    set: (value: Map<string[], boolean>) => {
      if (!container.value) {
        fallback.value = normalizeVisibleKeysMap(value);
        return;
      }
      storage.value.set(container.value.storageKey, normalizeVisibleKeysMap(value));
    },
  });
}

export const pinnedContainers = useProfileStorage("pinned", new Set<string>());
