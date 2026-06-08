function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((entry) => typeof entry === "string");
}

export function normalizeVisibleKeyPath(key: unknown): string[] {
  if (isStringArray(key)) {
    return key;
  }

  if (Array.isArray(key)) {
    return key.map((entry) => String(entry));
  }

  if (typeof key === "string") {
    return key.length > 0 ? key.split(".") : [];
  }

  if (key && typeof key === "object") {
    const values = Object.entries(key as Record<string, unknown>)
      .sort(([a], [b]) => Number(a) - Number(b))
      .map(([, value]) => String(value));

    if (values.length > 0) {
      return values;
    }
  }

  if (key == null) {
    return [];
  }

  return [String(key)];
}

export function normalizeVisibleKeysMap(value: unknown): Map<string[], boolean> {
  if (!(value instanceof Map)) {
    return new Map<string[], boolean>();
  }

  const normalized = new Map<string[], boolean>();
  for (const [key, enabled] of value.entries()) {
    normalized.set(normalizeVisibleKeyPath(key), enabled !== false);
  }
  return normalized;
}

export function hasNormalizedVisibleKeys(value: unknown): value is Map<string[], boolean> {
  return value instanceof Map && Array.from(value.keys()).every((key) => isStringArray(key));
}
