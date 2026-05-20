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
                  <input
                    type="time"
                    v-model="quietHours.start"
                    class="input input-sm focus:input-primary"
                    @change="saveQuietHours"
                  />
                </div>
                <span class="text-base-content/40 mt-5">→</span>
                <div>
                  <label class="label text-sm">{{ $t("notifications.settings.quiet-end") }}</label>
                  <input
                    type="time"
                    v-model="quietHours.end"
                    class="input input-sm focus:input-primary"
                    @change="saveQuietHours"
                  />
                </div>
              </div>
              <p class="text-base-content/50 text-xs">{{ $t("notifications.settings.quiet-hours-hint") }}</p>

              <!-- Stacking settings -->
              <div class="divider my-2 text-xs">Stacking</div>
              <div class="flex items-center gap-3">
                <div>
                  <label class="label text-sm">Stack threshold</label>
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
                  <label class="label text-sm">Stack window (min)</label>
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
                  <label class="label text-sm">Stacked priority</label>
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
              <div class="divider my-2 text-xs">Topic routing</div>
              <div>
                <label class="label text-sm">Quiet topic (optional)</label>
                <input
                  type="text"
                  v-model="quietHours.quietTopic"
                  placeholder="alerts-quiet"
                  class="input input-sm focus:input-primary w-full"
                  @change="saveQuietHours"
                />
              </div>
              <label class="flex cursor-pointer items-center gap-3 mt-2">
                <input
                  type="checkbox"
                  v-model="quietHours.stackedUsesQuietTopic"
                  class="checkbox checkbox-primary checkbox-sm"
                  @change="saveQuietHours"
                />
                <span class="text-sm">Send stacked alerts to quiet topic</span>
              </label>

              <!-- Timezone -->
              <div class="divider my-2 text-xs">Timezone</div>
              <div>
                <label class="label text-sm">Timezone</label>
                <input
                  type="text"
                  v-model="quietHours.timezone"
                  placeholder="Europe/Prague"
                  class="input input-sm focus:input-primary w-full"
                  @change="saveQuietHours"
                />
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
        <div class="tabs tabs-box mb-6">
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

        <!-- Alerts List -->
        <div class="space-y-4">
          <AlertCard
            v-for="alert in filteredAlerts"
            :key="alert.id"
            :alert="alert"
            :on-updated="fetchAlerts"
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

const { t } = useI18n();
const showDrawer = useDrawer();
const router = useRouter();
const route = useRoute();

// State
const alerts = ref<NotificationRule[]>([]);
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
});

async function fetchAlerts() {
  const res = await fetch(withBase("/api/notifications/rules"));
  alerts.value = await res.json();
}

async function fetchDispatchers() {
  const res = await fetch(withBase("/api/notifications/dispatchers"));
  dispatchers.value = await res.json();
}

async function fetchQuietHours() {
  const res = await fetch(withBase("/api/notifications/quiet-hours"));
  if (res.ok) {
    const data = await res.json();
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

// Local state
const filter = ref<"all" | "enabled" | "paused">("all");

const enabledCount = computed(() => alerts.value.filter((a) => a.enabled).length);
const pausedCount = computed(() => alerts.value.filter((a) => !a.enabled).length);

const filteredAlerts = computed(() => {
  if (filter.value === "enabled") return alerts.value.filter((a) => a.enabled);
  if (filter.value === "paused") return alerts.value.filter((a) => !a.enabled);
  return alerts.value;
});

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
