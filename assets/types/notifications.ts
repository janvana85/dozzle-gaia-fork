export interface NotificationRule {
  id: number;
  name: string;
  alertGroup?: string;
  enabled: boolean;
  containerExpression: string;
  logExpression: string;
  metricExpression?: string;
  eventExpression?: string;
  cooldown?: number;
  sampleWindow?: number;
  pausedUntil?: string;
  deliveryDays?: string[];
  triggerCount: number;
  triggeredContainers: number;
  lastTriggeredAt: string | null;
  dispatcher: Dispatcher | null;
  // ntfy per-rule routing
  ntfyTopic?: string;
  ntfyPriority?: number;
  ntfyTags?: string[];
  bypassQuietHours?: boolean;
  quietPriority?: number;
  holdDuringQuiet?: boolean;
  holdClearWindow?: number;
  burstCount?: number;
  burstWindow?: number;
  burstPriority?: number;
  burstNtfyTopic?: string;
  uniqueKeyRegex?: string;
  uniqueWindow?: number;
  uniqueThreshold?: number;
  // watchdog / coupled messages
  watchdogPattern?: string;
  watchdogWindow?: number; // seconds
  watchdogCooldown?: number;
  watchdogTriggerMessage?: string;
  watchdogClearMessage?: string;
  restartLoopEnabled?: boolean;
  restartLoopStateWindow?: number;
  restartLoopEventCount?: number;
  restartLoopEventWindow?: number;
  restartLoopCooldown?: number;
  restartLoopTriggerMessage?: string;
  // per-alert quiet hours override
  alertQuietEnabled?: boolean;
  alertQuietStart?: string;
  alertQuietEnd?: string;
  alertQuietTimezone?: string;
  // per-alert quiet-hours stacking override (0 = use global)
  quietStackThreshold?: number;
  quietStackWindow?: number;
}

export interface Dispatcher {
  id: number;
  name: string;
  type: string;
  url?: string;
  template?: string;
  headers?: Record<string, string>;
  prefix?: string;
  expiresAt?: string;
  // ntfy-specific (no token returned by API)
  topic?: string;
  priority?: number;
  tags?: string[];
  tokenSet?: boolean; // true if an auth token is configured (token value never returned)
  titleTemplate?: string;
  messageTemplate?: string;
}

export interface NotificationRuleInput {
  name: string;
  alertGroup?: string;
  enabled: boolean;
  dispatcherId: number;
  logExpression: string;
  containerExpression: string;
  metricExpression?: string;
  eventExpression?: string;
  cooldown?: number;
  sampleWindow?: number;
  pausedUntil?: string;
  deliveryDays?: string[];
  ntfyTopic?: string;
  ntfyPriority?: number;
  ntfyTags?: string[];
  bypassQuietHours?: boolean;
  quietPriority?: number;
  holdDuringQuiet?: boolean;
  holdClearWindow?: number;
  burstCount?: number;
  burstWindow?: number;
  burstPriority?: number;
  burstNtfyTopic?: string;
  uniqueKeyRegex?: string;
  uniqueWindow?: number;
  uniqueThreshold?: number;
  watchdogPattern?: string;
  watchdogWindow?: number;
  watchdogCooldown?: number;
  watchdogTriggerMessage?: string;
  watchdogClearMessage?: string;
  restartLoopEnabled?: boolean;
  restartLoopStateWindow?: number;
  restartLoopEventCount?: number;
  restartLoopEventWindow?: number;
  restartLoopCooldown?: number;
  restartLoopTriggerMessage?: string;
  alertQuietEnabled?: boolean;
  alertQuietStart?: string;
  alertQuietEnd?: string;
  alertQuietTimezone?: string;
  quietStackThreshold?: number;
  quietStackWindow?: number;
}

export interface QuietHoursConfig {
  enabled: boolean;
  start: string;
  end: string;
  timezone?: string;
  stackThreshold?: number;
  stackWindow?: number;
  stackedPriority?: number;
  quietTopic?: string;
  stackedUsesQuietTopic?: boolean;
}

export interface PreviewResult {
  containerError?: string;
  logError?: string;
  metricError?: string;
  eventError?: string;
  uniqueRegexError?: string;
  matchedContainers: {
    id: string;
    name: string;
    image: string;
    host: string;
  }[];
  matchedLogs: {
    id: number;
    t: string;
    m: unknown;
    rm: string;
    ts: number;
    l: string;
    s: string;
  }[];
  uniqueMatches?: {
    key: string;
    message: string;
  }[];
  totalLogs: number;
  messageKeys?: string[];
}

export interface TestWebhookResult {
  success: boolean;
  statusCode?: number;
  error?: string;
}

export interface CloudConfig {
  prefix: string;
  expiresAt?: string;
  linked: boolean;
  streamLogs: boolean;
}

export interface CloudStatus {
  user: { email: string; name: string };
  plan: { name: string; events_per_month: number; retention_days: number };
  usage: { events_used: number; events_limit: number; period: string };
}
