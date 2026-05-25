export interface NotificationRule {
  id: number;
  name: string;
  enabled: boolean;
  containerExpression: string;
  logExpression: string;
  metricExpression?: string;
  eventExpression?: string;
  cooldown?: number;
  sampleWindow?: number;
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
  // watchdog / coupled messages
  watchdogPattern?: string;
  watchdogWindow?: number; // seconds
  watchdogCooldown?: number;
  watchdogTriggerMessage?: string;
  watchdogClearMessage?: string;
  // per-alert quiet hours override
  alertQuietEnabled?: boolean;
  alertQuietStart?: string;
  alertQuietEnd?: string;
  alertQuietTimezone?: string;
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
}

export interface NotificationRuleInput {
  name: string;
  enabled: boolean;
  dispatcherId: number;
  logExpression: string;
  containerExpression: string;
  metricExpression?: string;
  eventExpression?: string;
  cooldown?: number;
  sampleWindow?: number;
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
  watchdogPattern?: string;
  watchdogWindow?: number;
  watchdogCooldown?: number;
  watchdogTriggerMessage?: string;
  watchdogClearMessage?: string;
  alertQuietEnabled?: boolean;
  alertQuietStart?: string;
  alertQuietEnd?: string;
  alertQuietTimezone?: string;
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
