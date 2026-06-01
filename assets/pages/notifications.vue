<template>
  <PageWithLinks>
    <section>
      <!-- Header -->
      <div class="mb-8">
        <h2 class="text-2xl font-bold">{{ $t("notifications.title") }}</h2>
        <p class="text-base-content/60">{{ $t("notifications.description") }}</p>
      </div>

      <!-- Quiet Hours Settings -->
      <div class="mb-8">
        <h3 class="text-base-content/60 mb-4 font-semibold tracking-wide uppercase">
          {{ $t("notifications.settings.title") }}
        </h3>
        <div class="card bg-base-200 p-4 md:w-96">
          <div class="space-y-3">
            <label class="flex cursor-pointer items-center gap-3">
              <input
                type="checkbox"
                v-model="quietHours.enabled"
                class="checkbox checkbox-primary"
                @change="saveQuietHours"
              />
              <span class="font-medium">{{ $t("notifications.settings.quiet-hours-enabled") }}</span>
            </label>
            <template v-if="quietHours.enabled">
              <div class="flex items-center gap-3">
                <div>
                  <label class="label text-sm">{{ $t("notifications.settings.quiet-start") }}</label>
                  <QuietTimeInput
                    v-model="quietHours.start"
                    :label="$t('notifications.settings.quiet-start')"
                    @change="saveQuietHours"
                  />
                </div>
                <span class="text-base-content/40 mt-5">→</span>
                <div>
                  <label class="label text-sm">{{ $t("notifications.settings.quiet-end") }}</label>
                  <QuietTimeInput
                    v-model="quietHours.end"
                    :label="$t('notifications.settings.quiet-end')"
                    @change="saveQuietHours"
                  />
                </div>
              </div>
              <p class="text-base-content/50 text-xs">{{ $t("notifications.settings.quiet-hours-hint") }}</p>

              <!-- Stacking settings -->
              <div class="divider my-2 text-xs">{{ $t("notifications.settings.stacking") }}</div>
              <div class="flex items-center gap-3">
                <div>
                  <label class="label text-sm">{{ $t("notifications.settings.stack-threshold") }}</label>
                  <input
                    type="number"
                    v-model.number="quietHours.stackThreshold"
                    min="2"
                    max="20"
                    class="input input-sm focus:input-primary w-24"
                    @change="saveQuietHours"
                  />
                </div>
                <div>
                  <label class="label text-sm">{{ $t("notifications.settings.stack-window") }}</label>
                  <input
                    type="number"
                    v-model.number="quietHours.stackWindow"
                    min="1"
                    max="1440"
                    class="input input-sm focus:input-primary w-24"
                    @change="saveQuietHours"
                  />
                </div>
                <div>
                  <label class="label text-sm">{{ $t("notifications.settings.stacked-priority") }}</label>
                  <input
                    type="number"
                    v-model.number="quietHours.stackedPriority"
                    min="1"
                    max="5"
                    class="input input-sm focus:input-primary w-24"
                    @change="saveQuietHours"
                  />
                </div>
              </div>

              <!-- Topic routing -->
              <div class="divider my-2 text-xs">{{ $t("notifications.settings.topic-routing") }}</div>
              <div>
                <label class="label text-sm">{{ $t("notifications.settings.quiet-topic") }}</label>
                <input
                  type="text"
                  v-model="quietHours.quietTopic"
                  placeholder="alerts-quiet"
                  class="input input-sm focus:input-primary w-full"
                  @change="saveQuietHours"
                />
              </div>
              <label class="mt-2 flex cursor-pointer items-center gap-3">
                <input
                  type="checkbox"
                  v-model="quietHours.stackedUsesQuietTopic"
                  class="checkbox checkbox-primary checkbox-sm"
                  @change="saveQuietHours"
                />
                <span class="text-sm">{{ $t("notifications.settings.stacked-uses-quiet-topic") }}</span>
              </label>

              <!-- Timezone -->
              <div class="divider my-2 text-xs">{{ $t("notifications.settings.timezone-section") }}</div>
              <div>
                <label class="label text-sm">{{ $t("notifications.settings.timezone") }}</label>
                <input
                  type="text"
                  v-model="quietHours.timezone"
                  placeholder="Europe/Prague"
                  class="input input-sm focus:input-primary w-full"
                  @change="saveQuietHours"
                />
                <p v-if="serverNowLabel" class="text-base-content/50 mt-2 text-xs">
                  {{ $t("notifications.settings.server-now") }}: {{ serverNowLabel }}
                </p>
                <p v-if="quietHoursActiveLabel" class="text-base-content/50 text-xs">
                  {{ $t("notifications.settings.quiet-hours-active-now") }}: {{ quietHoursActiveLabel }}
                </p>
              </div>
            </template>
          </div>
        </div>
      </div>

      <!-- Destinations Section -->
      <div class="mb-8">
        <h3 class="text-base-content/60 mb-4 font-semibold tracking-wide uppercase">
          {{ $t("notifications.destinations") }}
        </h3>

        <template v-if="dispatchers.length === 0">
          <p class="text-base-content/60 mb-4 text-sm">{{ $t("notifications.empty-state.description") }}</p>
          <button
            class="card card-border border-base-content/30 hover:border-base-content/50 w-full cursor-pointer border-dashed transition-colors md:w-72"
            @click="openAddDestination"
          >
            <div class="card-body items-center justify-center gap-1 p-4">
              <mdi:plus class="text-2xl" />
              <span class="text-base-content/60 text-sm">{{ $t("notifications.add-destination") }}</span>
            </div>
          </button>
        </template>

        <template v-else>
          <div class="flex flex-wrap gap-4">
            <DestinationCard
              v-for="dest in dispatchers"
              :key="dest.id"
              :destination="dest"
              :on-updated="fetchAll"
              :existing-dispatchers="dispatchers"
              class="w-full md:w-72"
            />
            <!-- Add Destination Card -->
            <button
              class="card card-border border-base-content/30 hover:border-base-content/50 w-full cursor-pointer border-dashed transition-colors md:w-72"
              @click="openAddDestination"
            >
              <div class="card-body items-center justify-center gap-1 p-4">
                <mdi:plus class="text-2xl" />
                <span class="text-base-content/60 text-sm">{{ $t("notifications.add-destination") }}</span>
              </div>
            </button>
          </div>
        </template>
      </div>

      <!-- Alerts Section -->
      <div>
        <div class="mb-4">
          <h3 class="text-base-content/60 font-semibold tracking-wide uppercase">{{ $t("notifications.alerts") }}</h3>
        </div>

        <!-- Filter Tabs -->
        <div class="mb-6 flex flex-wrap items-center justify-between gap-3">
          <div class="flex min-w-0 flex-1 flex-wrap items-center gap-3">
            <div class="tabs tabs-box">
              <button class="tab" :class="{ 'tab-active': filter === 'all' }" @click="filter = 'all'">
                {{ $t("notifications.filter.all", { count: alerts.length }) }}
              </button>
              <button class="tab" :class="{ 'tab-active': filter === 'enabled' }" @click="filter = 'enabled'">
                {{ $t("notifications.filter.enabled", { count: enabledCount }) }}
              </button>
              <button class="tab" :class="{ 'tab-active': filter === 'paused' }" @click="filter = 'paused'">
                {{ $t("notifications.filter.paused", { count: pausedCount }) }}
              </button>
            </div>
            <label class="input input-sm input-bordered flex w-full max-w-md items-center gap-2 md:w-80">
              <mdi:magnify class="text-base-content/50 shrink-0" />
              <input
                v-model="alertSearch"
                type="search"
                class="grow"
                :placeholder="$t('notifications.search-placeholder')"
              />
            </label>
          </div>
          <div class="join">
            <button
              class="btn btn-sm join-item"
              :class="{ 'btn-active': alertViewMode === 'list' }"
              @click="alertViewMode = 'list'"
            >
              <mdi:view-list class="text-base" />
              {{ $t("notifications.grouping.list") }}
            </button>
            <button
              class="btn btn-sm join-item"
              :class="{ 'btn-active': alertViewMode === 'grouped' }"
              @click="alertViewMode = 'grouped'"
            >
              <mdi:folder-multiple-outline class="text-base" />
              {{ $t("notifications.grouping.grouped") }}
            </button>
          </div>
        </div>

        <!-- Alerts List -->
        <div v-if="alertViewMode === 'grouped'" class="space-y-4">
          <details
            v-for="group in groupedAlerts"
            :key="group.key"
            class="collapse-arrow bg-base-100 rounded-box collapse shadow-sm"
            open
          >
            <summary class="collapse-title px-5 py-4">
              <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
                <div class="min-w-0">
                  <div class="flex min-w-0 items-center gap-2">
                    <mdi:folder-multiple-outline class="text-info shrink-0" />
                    <h4 class="truncate text-base font-semibold">{{ group.label }}</h4>
                  </div>
                  <div class="text-base-content/60 mt-1 text-xs">{{ group.description }}</div>
                </div>
                <div class="ml-auto flex shrink-0 flex-wrap items-center gap-2 pr-6">
                  <span class="badge badge-neutral badge-sm">
                    {{ $t("notifications.grouping.alert-count", { count: group.alerts.length }) }}
                  </span>
                  <span v-if="group.pausedCount" class="badge badge-warning badge-sm">
                    {{ $t("notifications.grouping.paused-count", { count: group.pausedCount }) }}
                  </span>
                  <span v-if="group.triggeredCount" class="badge badge-ghost badge-sm">
                    {{ $t("notifications.grouping.triggered-count", { count: group.triggeredCount }) }}
                  </span>
                </div>
              </div>
            </summary>
            <div class="collapse-content space-y-3 px-4 pb-4">
              <AlertCard
                v-for="alert in group.alerts"
                :key="alert.id"
                :alert="alert"
                :on-updated="fetchAlerts"
                :on-duplicated="placeDuplicatedAlert"
                :highlight="alert.id === highlightId"
              />
            </div>
          </details>
          <button
            class="card card-border border-base-content/30 hover:border-base-content/50 w-full cursor-pointer border-dashed transition-colors"
            @click="openCreateAlert"
          >
            <div class="card-body items-center justify-center gap-1 p-4">
              <mdi:plus class="text-2xl" />
              <span class="text-base-content/60 text-sm">{{ $t("notifications.add-alert") }}</span>
            </div>
          </button>
        </div>
        <div v-else class="space-y-4">
          <AlertCard
            v-for="alert in filteredAlerts"
            :key="alert.id"
            :alert="alert"
            :on-updated="fetchAlerts"
            :on-duplicated="placeDuplicatedAlert"
            :highlight="alert.id === highlightId"
          />
          <button
            class="card card-border border-base-content/30 hover:border-base-content/50 w-full cursor-pointer border-dashed transition-colors"
            @click="openCreateAlert"
          >
            <div class="card-body items-center justify-center gap-1 p-4">
              <mdi:plus class="text-2xl" />
              <span class="text-base-content/60 text-sm">{{ $t("notifications.add-alert") }}</span>
            </div>
          </button>
        </div>
      </div>
    </section>
  </PageWithLinks>
</template>

<script lang="ts" setup>
import type { NotificationRule, Dispatcher } from "@/types/notifications";
import AlertForm from "@/components/Notification/AlertForm.vue";
import DestinationForm from "@/components/Notification/DestinationForm.vue";
import QuietTimeInput from "@/components/Notification/QuietTimeInput.vue";

const { t } = useI18n();
const showDrawer = useDrawer();
const router = useRouter();
const route = useRoute();

// State
const alerts = ref<NotificationRule[]>([]);
const alertOrder = ref<number[]>([]);
const dispatchers = ref<Dispatcher[]>([]);
const quietHours = ref({
  enabled: false,
  start: "22:00",
  end: "08:00",
  timezone: "",
  stackThreshold: 3,
  stackWindow: 15,
  stackedPriority: 4,
  quietTopic: "",
  stackedUsesQuietTopic: false,
  activeNow: false,
});
const serverNowLabel = ref("");

function orderedAlerts(data: NotificationRule[]) {
  const byId = new Map(data.map((alert) => [alert.id, alert]));
  const orderedIds = alertOrder.value.filter((id) => byId.has(id));
  const knownIds = new Set(orderedIds);
  const newIds = data.map((alert) => alert.id).filter((id) => !knownIds.has(id));

  alertOrder.value = [...orderedIds, ...newIds];
  return alertOrder.value.map((id) => byId.get(id)).filter((alert): alert is NotificationRule => !!alert);
}

async function fetchAlerts() {
  const res = await fetch(withBase("/api/notifications/rules"));
  const data: NotificationRule[] = await res.json();
  alerts.value = orderedAlerts(data);
}

async function fetchDispatchers() {
  const res = await fetch(withBase("/api/notifications/dispatchers"));
  dispatchers.value = await res.json();
}

async function fetchQuietHours() {
  const res = await fetch(withBase("/api/notifications/quiet-hours"));
  if (res.ok) {
    const data = await res.json();
    serverNowLabel.value = data.serverNowLabel || "";
    quietHours.value = {
      ...data,
      stackWindow: Math.round((data.stackWindow || 900) / 60),
    };
  }
}

async function saveQuietHours() {
  const payload = {
    ...quietHours.value,
    stackWindow: quietHours.value.stackWindow * 60,
  };
  await fetch(withBase("/api/notifications/quiet-hours"), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

async function fetchAll() {
  await Promise.all([fetchAlerts(), fetchDispatchers(), fetchQuietHours()]);
}

const highlightId = ref<number | null>(null);
const { showToast } = useToast();

function consumeHighlight(value: unknown) {
  if (typeof value !== "string" || !value) return false;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed)) return false;
  highlightId.value = parsed;
  router.replace({ query: {} });
  showToast(
    {
      type: "info",
      message: t("notifications.default-alert-created"),
    },
    { expire: 8000 },
  );
  return true;
}

function consumeAction(action: unknown) {
  if (action !== "create-alert") return;
  router.replace({ query: {} });
  openCreateAlertPrefilled();
}

onMounted(async () => {
  await fetchAll();
  const hash = window.location.hash;
  if (hash === "#cloudLinked") {
    router.replace({ hash: "" });
  }

  if (!consumeHighlight(route.query.highlight)) {
    consumeAction(route.query.action);
  }
});

watch(
  () => route.query.highlight,
  (value) => consumeHighlight(value),
);

watch(
  () => route.query.action,
  (action) => consumeAction(action),
);

const quietHoursActiveLabel = computed(() => {
  if (typeof quietHours.value.activeNow !== "boolean") return "";
  return quietHours.value.activeNow ? "Yes" : "No";
});

// Local state
const filter = ref<"all" | "enabled" | "paused">("all");
const alertViewMode = useStorage<"list" | "grouped">("DOZZLE_ALERT_VIEW_MODE", "grouped");
const alertSearch = ref("");

function isAlertPaused(alert: NotificationRule) {
  return !alert.enabled || Boolean(alert.pausedUntil && new Date(alert.pausedUntil) > new Date());
}

const enabledCount = computed(() => alerts.value.filter((a) => !isAlertPaused(a)).length);
const pausedCount = computed(() => alerts.value.filter(isAlertPaused).length);

const filteredAlerts = computed(() => {
  const base =
    filter.value === "enabled"
      ? alerts.value.filter((a) => !isAlertPaused(a))
      : filter.value === "paused"
        ? alerts.value.filter(isAlertPaused)
        : alerts.value;
  const query = alertSearch.value.trim().toLowerCase();
  if (!query) return base;
  return base.filter((alert) => alertSearchText(alert).includes(query));
});

type AlertGroup = {
  key: string;
  label: string;
  description: string;
  alerts: NotificationRule[];
  pausedCount: number;
  triggeredCount: number;
};

const groupedAlerts = computed<AlertGroup[]>(() => {
  const groups = new Map<string, AlertGroup>();
  for (const alert of filteredAlerts.value) {
    const groupName = alert.alertGroup?.trim();
    const key = groupName ? `group:${groupName.toLowerCase()}` : "__ungrouped__";
    const group =
      groups.get(key) ??
      ({
        key,
        label: groupName || t("notifications.grouping.ungrouped"),
        description: groupName
          ? t("notifications.grouping.manual-group")
          : t("notifications.grouping.ungrouped-description"),
        alerts: [],
        pausedCount: 0,
        triggeredCount: 0,
      } satisfies AlertGroup);

    group.alerts.push(alert);
    if (isAlertPaused(alert)) group.pausedCount += 1;
    group.triggeredCount += alert.triggerCount || 0;
    groups.set(key, group);
  }
  return [...groups.values()];
});

function alertSearchText(alert: NotificationRule) {
  return [
    alert.name,
    alert.alertGroup,
    alert.dispatcher?.name,
    alert.dispatcher?.type,
    alert.containerExpression,
    alert.logExpression,
    alert.metricExpression,
    alert.eventExpression,
  ]
    .filter(Boolean)
    .join("\n")
    .toLowerCase();
}

async function placeDuplicatedAlert(sourceId: number, duplicate: NotificationRule) {
  const currentOrder = alertOrder.value.filter((id) => id !== duplicate.id);
  const sourceIndex = currentOrder.indexOf(sourceId);
  if (sourceIndex === -1) {
    currentOrder.push(duplicate.id);
  } else {
    currentOrder.splice(sourceIndex + 1, 0, duplicate.id);
  }
  alertOrder.value = currentOrder;
  await fetchAlerts();
  highlightId.value = duplicate.id;
}

function openCreateAlert() {
  showDrawer(AlertForm, { onCreated: fetchAlerts }, "lg");
}

function openCreateAlertPrefilled() {
  const cloudDispatcher = dispatchers.value.find((d) => d.type === "cloud");
  showDrawer(
    AlertForm,
    {
      onCreated: fetchAlerts,
      prefill: {
        name: t("notifications.prefill-name"),
        logExpression: t("notifications.prefill-expression"),
        ...(cloudDispatcher ? { dispatcherId: cloudDispatcher.id } : {}),
      },
    },
    "lg",
  );
}

function openAddDestination() {
  showDrawer(
    DestinationForm,
    {
      onCreated: fetchDispatchers,
    },
    "md",
  );
}
</script>
