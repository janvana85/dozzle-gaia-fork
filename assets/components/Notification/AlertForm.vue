<template>
  <div class="space-y-4 p-4">
    <div class="mb-6">
      <h2 class="text-2xl font-bold">
        {{ isEditing ? $t("notifications.alert-form.edit-title") : $t("notifications.alert-form.create-title") }}
      </h2>
      <p class="text-base-content/60">{{ $t("notifications.alert-form.description") }}</p>
    </div>

    <!-- Alert Name -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.alert-name") }}</legend>
      <input
        ref="alertNameInput"
        v-model="alertName"
        type="text"
        class="input focus:input-primary w-full text-base"
        :class="alertName.trim() ? 'input-primary' : ''"
        required
        :placeholder="$t('notifications.alert-form.alert-name-placeholder')"
      />
    </fieldset>

    <!-- Alert Type Toggle -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.alert-type") }}</legend>
      <div class="flex gap-2">
        <button
          class="btn btn-sm"
          :class="alertType === 'log' ? 'btn-primary' : 'btn-outline'"
          @click="alertType = 'log'"
        >
          <mdi:text-box-outline class="mr-1" />
          {{ $t("notifications.alert-form.log-alert") }}
        </button>
        <button
          class="btn btn-sm"
          :class="alertType === 'metric' ? 'btn-primary' : 'btn-outline'"
          @click="alertType = 'metric'"
        >
          <mdi:chart-line class="mr-1" />
          {{ $t("notifications.alert-form.metric-alert") }}
        </button>
        <button
          class="btn btn-sm"
          :class="alertType === 'event' ? 'btn-primary' : 'btn-outline'"
          @click="alertType = 'event'"
        >
          <mdi:bell-ring-outline class="mr-1" />
          {{ $t("notifications.alert-form.event-alert") }}
        </button>
      </div>
    </fieldset>

    <!-- Container Filter -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.container-filter") }}</legend>
      <div
        class="input focus-within:input-primary h-auto w-full focus-within:z-50"
        :class="
          containerExpression.trim() && !containerResult?.error
            ? 'input-primary'
            : { 'input-error!': containerResult?.error }
        "
      >
        <div ref="containerEditorRef" class="w-full"></div>
      </div>
      <div v-if="containerResult" class="fieldset-label">
        <span v-if="containerResult.error" class="text-error">{{ containerResult.error }}</span>
        <span v-else-if="containerResult.containers?.length" class="text-success">
          <mdi:check class="inline" />
          {{
            $t("notifications.alert-form.containers-match", {
              count: containerResult.containers.length,
              names: containerResult.containers.map((c) => c.name).join(", "),
            })
          }}
        </span>
        <span v-else class="text-warning">
          <mdi:alert class="inline" />
          {{ $t("notifications.alert-form.no-containers-match") }}
        </span>
      </div>
    </fieldset>

    <!-- Type-specific fields -->
    <KeepAlive>
      <LogAlertFields
        v-if="alertType === 'log'"
        ref="fieldsRef"
        :alert="alert"
        :prefill="prefill"
        :container-expression="containerExpression"
        :is-loading="isLoading"
        :validate-preview="validatePreview"
      />
      <MetricAlertFields
        v-else-if="alertType === 'metric'"
        ref="fieldsRef"
        :alert="alert"
        :prefill="prefill"
        :container-expression="containerExpression"
        :is-loading="isLoading"
        :validate-preview="validatePreview"
      />
      <EventAlertFields
        v-else
        ref="fieldsRef"
        :alert="alert"
        :prefill="prefill"
        :container-expression="containerExpression"
        :is-loading="isLoading"
        :validate-preview="validatePreview"
      />
    </KeepAlive>

    <!-- Destination -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.destination") }}</legend>
      <details class="dropdown w-full" ref="destinationDropdown">
        <summary class="btn btn-outline w-full justify-between" :class="{ 'btn-primary': selectedDestination }">
          <span class="flex items-center gap-2">
            <template v-if="selectedDestination">
              <mdi:webhook v-if="selectedDestination.type === 'webhook'" />
              <mdi:bell-ring v-else-if="selectedDestination.type === 'ntfy'" />
              <mdi:cloud v-else />
              {{ selectedDestination.name }}
            </template>
            <span v-else class="text-base-content/60">{{ $t("notifications.alert-form.select-destination") }}</span>
          </span>
          <carbon:caret-down />
        </summary>
        <ul class="dropdown-content menu bg-base-200 rounded-box z-50 mt-1 w-full border p-2 shadow-sm">
          <li v-for="dest in destinations" :key="dest.id">
            <a
              @click="
                dispatcherId = dest.id;
                destinationDropdown?.removeAttribute('open');
              "
              :class="{ active: dispatcherId === dest.id }"
            >
              <mdi:webhook v-if="dest.type === 'webhook'" />
              <mdi:bell-ring v-else-if="dest.type === 'ntfy'" />
              <mdi:cloud v-else />
              {{ dest.name }}
            </a>
          </li>
        </ul>
      </details>
      <div v-if="!destinations.length" class="fieldset-label">
        <span class="text-warning">
          <mdi:alert class="inline" />
          {{ $t("notifications.alert-form.no-destinations") }}
        </span>
      </div>
    </fieldset>

    <!-- ntfy Routing Options (shown when ntfy dispatcher selected) -->
    <template v-if="selectedDestination?.type === 'ntfy'">
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.ntfy-options") }}</legend>
        <div class="space-y-3">
          <!-- Topic Override -->
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.ntfy-topic-override") }}</label>
            <input
              v-model="ntfyTopic"
              type="text"
              class="input focus:input-primary w-full text-base"
              :placeholder="selectedDestination.topic ?? 'dozzle-alerts'"
            />
          </div>
          <!-- Priority Override -->
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.ntfy-priority-override") }}</label>
            <select v-model="ntfyPriority" class="select focus:select-primary w-full text-base">
              <option :value="0">{{ $t("notifications.alert-form.ntfy-priority-default") }}</option>
              <option :value="1">{{ $t("notifications.destination-form.ntfy-priority-1") }}</option>
              <option :value="2">{{ $t("notifications.destination-form.ntfy-priority-2") }}</option>
              <option :value="3">{{ $t("notifications.destination-form.ntfy-priority-3") }}</option>
              <option :value="4">{{ $t("notifications.destination-form.ntfy-priority-4") }}</option>
              <option :value="5">{{ $t("notifications.destination-form.ntfy-priority-5") }}</option>
            </select>
          </div>
          <!-- Tags -->
          <div>
            <label class="label text-sm">
              {{ $t("notifications.alert-form.ntfy-tags") }}
              <span class="text-base-content/50 ml-1 text-xs">{{ $t("notifications.alert-form.ntfy-tags-hint") }}</span>
            </label>
            <input
              v-model="ntfyTagsInput"
              type="text"
              class="input focus:input-primary w-full text-base"
              placeholder="warning,container"
            />
          </div>
        </div>
      </fieldset>

      <!-- Burst Detection -->
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.burst-detection") }}</legend>
        <div class="grid grid-cols-3 gap-2">
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.burst-count") }}</label>
            <input
              v-model.number="burstCount"
              type="number"
              min="0"
              class="input focus:input-primary w-full"
              placeholder="0"
            />
          </div>
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.burst-window") }}</label>
            <input
              v-model.number="burstWindow"
              type="number"
              min="0"
              class="input focus:input-primary w-full"
              placeholder="60"
            />
          </div>
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.burst-priority") }}</label>
            <select v-model.number="burstPriority" class="select focus:select-primary w-full">
              <option :value="0">-</option>
              <option :value="4">{{ $t("notifications.destination-form.ntfy-priority-4") }}</option>
              <option :value="5">{{ $t("notifications.destination-form.ntfy-priority-5") }}</option>
            </select>
          </div>
        </div>
      </fieldset>

      <!-- Quiet Hours -->
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.quiet-hours") }}</legend>
        <div class="space-y-2">
          <label class="flex cursor-pointer items-center gap-2">
            <input type="checkbox" v-model="bypassQuietHours" class="checkbox checkbox-primary" />
            <span class="text-sm">{{ $t("notifications.alert-form.bypass-quiet-hours") }}</span>
          </label>
          <label class="flex cursor-pointer items-center gap-2">
            <input
              type="checkbox"
              v-model="holdDuringQuiet"
              :disabled="bypassQuietHours"
              class="checkbox checkbox-primary"
            />
            <span class="text-sm" :class="bypassQuietHours ? 'opacity-40' : ''">{{
              $t("notifications.alert-form.hold-during-quiet")
            }}</span>
          </label>
          <div v-if="!bypassQuietHours && !holdDuringQuiet">
            <label class="label text-sm">{{ $t("notifications.alert-form.quiet-priority") }}</label>
            <select v-model.number="quietPriority" class="select focus:select-primary w-full text-base">
              <option :value="0">{{ $t("notifications.alert-form.ntfy-priority-default") }}</option>
              <option :value="1">{{ $t("notifications.destination-form.ntfy-priority-1") }}</option>
              <option :value="2">{{ $t("notifications.destination-form.ntfy-priority-2") }}</option>
              <option :value="3">{{ $t("notifications.destination-form.ntfy-priority-3") }}</option>
            </select>
          </div>
        </div>
      </fieldset>

      <!-- Hold/Clear Window -->
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-lg">
          {{ $t("notifications.alert-form.hold-clear-window") }}
          <span class="text-base-content/50 ml-2 text-xs font-normal">{{
            $t("notifications.alert-form.hold-clear-hint")
          }}</span>
        </legend>
        <input
          v-model.number="holdClearWindow"
          type="number"
          min="0"
          class="input focus:input-primary w-full"
          placeholder="0"
        />
      </fieldset>
    </template>

    <!-- Pair Alert (log alerts only) -->
    <template v-if="alertType === 'log'">
      <fieldset class="fieldset">
        <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.watchdog-title") }}</legend>
        <label class="flex cursor-pointer items-center gap-2">
          <input type="checkbox" v-model="pairAlertEnabled" class="checkbox checkbox-primary" />
          <span class="text-sm">{{ $t("notifications.alert-form.pair-alert-enabled") }}</span>
        </label>
        <p class="text-base-content/50 mt-1 text-xs">{{ $t("notifications.alert-form.pair-alert-hint") }}</p>
        <div v-if="pairAlertEnabled" class="mt-3 space-y-3">
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.watchdog-window") }}</label>
            <div class="flex items-center gap-2">
              <input
                v-model.number="watchdogWindowMins"
                type="number"
                min="0"
                class="input focus:input-primary w-32"
                placeholder="0"
              />
              <span class="text-base-content/60 text-sm">{{
                $t("notifications.alert-form.watchdog-window-unit")
              }}</span>
            </div>
            <p class="text-base-content/50 mt-1 text-xs">{{ $t("notifications.alert-form.watchdog-window-hint") }}</p>
          </div>
          <div v-if="watchdogWindowMins > 0">
            <label class="label text-sm">{{ $t("notifications.alert-form.watchdog-pattern") }}</label>
            <input
              v-model="watchdogPattern"
              type="text"
              class="input focus:input-primary w-full text-base"
              :placeholder="$t('notifications.alert-form.watchdog-pattern-placeholder')"
            />
            <p class="text-base-content/50 mt-1 text-xs">{{ $t("notifications.alert-form.watchdog-pattern-hint") }}</p>
          </div>
          <div v-if="watchdogWindowMins > 0">
            <label class="label text-sm">Cooldown between alerts (min)</label>
            <input
              v-model.number="watchdogCooldownMins"
              type="number"
              min="0"
              class="input focus:input-primary w-32"
              placeholder="0"
            />
            <p class="text-base-content/50 mt-1 text-xs">Minimum minutes between repeated pair alerts. 0 = no cooldown.</p>
          </div>
          <div v-if="watchdogWindowMins > 0">
            <label class="label text-sm">Trigger message (optional)</label>
            <input
              v-model="watchdogTriggerMessage"
              type="text"
              class="input focus:input-primary w-full text-base"
              placeholder="Service is down"
            />
            <p class="text-base-content/50 mt-1 text-xs">Custom notification text when the trigger side times out. Leave blank for default.</p>
          </div>
          <div v-if="watchdogWindowMins > 0 && watchdogPattern">
            <label class="label text-sm">Clear message (optional)</label>
            <input
              v-model="watchdogClearMessage"
              type="text"
              class="input focus:input-primary w-full text-base"
              placeholder="Service recovered"
            />
            <p class="text-base-content/50 mt-1 text-xs">Sent when the clear filter matches before the max delay expires. Leave blank for no clear notification.</p>
          </div>
        </div>
      </fieldset>
    </template>

    <!-- Per-alert quiet hours override -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.alert-form.alert-quiet-title") }}</legend>
      <div class="space-y-3">
        <label class="flex cursor-pointer items-center gap-2">
          <input type="checkbox" v-model="alertQuietEnabled" class="checkbox checkbox-primary" />
          <span class="text-sm">{{ $t("notifications.alert-form.alert-quiet-enabled") }}</span>
        </label>
        <p class="text-base-content/50 mt-1 text-xs">{{ $t("notifications.alert-form.alert-quiet-hint") }}</p>
        <template v-if="alertQuietEnabled">
          <div class="flex items-center gap-3">
            <div>
              <label class="label text-sm">{{ $t("notifications.alert-form.alert-quiet-start") }}</label>
              <input
                type="time"
                v-model="alertQuietStart"
                class="input input-sm focus:input-primary"
              />
            </div>
            <span class="text-base-content/40 mt-5">→</span>
            <div>
              <label class="label text-sm">{{ $t("notifications.alert-form.alert-quiet-end") }}</label>
              <input
                type="time"
                v-model="alertQuietEnd"
                class="input input-sm focus:input-primary"
              />
            </div>
          </div>
          <div>
            <label class="label text-sm">{{ $t("notifications.alert-form.alert-quiet-timezone") }}</label>
            <input
              type="text"
              v-model="alertQuietTimezone"
              class="input input-sm focus:input-primary w-full"
              :placeholder="$t('notifications.alert-form.alert-quiet-timezone-placeholder')"
            />
          </div>
          <p class="text-base-content/50 text-xs">{{ $t("notifications.alert-form.alert-quiet-help") }}</p>
        </template>
      </div>
    </fieldset>

    <!-- Error -->
    <div v-if="saveError" class="alert alert-error">
      <span>{{ saveError }}</span>
    </div>

    <!-- Actions -->
    <div class="flex justify-end gap-2 pt-4">
      <button class="btn" @click="close?.()">{{ $t("notifications.alert-form.cancel") }}</button>
      <button class="btn btn-primary" :disabled="!canSave" @click="save">
        <span v-if="isSaving" class="loading loading-spinner loading-sm"></span>
        {{ isEditing ? $t("notifications.alert-form.save") : $t("notifications.alert-form.create") }}
      </button>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { useAlertForm } from "@/composable/alertForm";
import LogAlertFields from "./LogAlertFields.vue";
import MetricAlertFields from "./MetricAlertFields.vue";
import EventAlertFields from "./EventAlertFields.vue";
import type { NotificationRule } from "@/types/notifications";

const props = defineProps<{
  close?: () => void;
  onCreated?: () => void;
  alert?: NotificationRule;
  prefill?: {
    name?: string;
    containerExpression?: string;
    logExpression?: string;
    metricExpression?: string;
    eventExpression?: string;
    dispatcherId?: number;
  };
}>();

const {
  isEditing,
  alertName,
  containerExpression,
  dispatcherId,
  destinations,
  selectedDestination,
  containerResult,
  isLoading,
  isSaving,
  saveError,
  baseCanSave,
  setupContainerEditor,
  saveAlert,
  validatePreview,
} = useAlertForm(props);

// Template refs
const alertNameInput = ref<HTMLInputElement>();
const containerEditorRef = ref<HTMLElement>();
const destinationDropdown = ref<HTMLDetailsElement>();
const fieldsRef = ref<
  InstanceType<typeof LogAlertFields> | InstanceType<typeof MetricAlertFields> | InstanceType<typeof EventAlertFields>
>();
useFocus(alertNameInput, { initialValue: true });

// Alert type
const alertType = ref<"log" | "metric" | "event">(
  props.alert?.metricExpression ? "metric" : props.alert?.eventExpression ? "event" : "log",
);

// ntfy per-rule options
const ntfyTopic = ref(props.alert?.ntfyTopic ?? "");
const ntfyPriority = ref(props.alert?.ntfyPriority ?? 0);
const ntfyTagsInput = ref((props.alert?.ntfyTags ?? []).join(", "));
const bypassQuietHours = ref(props.alert?.bypassQuietHours ?? false);
const quietPriority = ref(props.alert?.quietPriority ?? 0);
const holdDuringQuiet = ref(props.alert?.holdDuringQuiet ?? false);
const holdClearWindow = ref(props.alert?.holdClearWindow ?? 0);
const burstCount = ref(props.alert?.burstCount ?? 0);
const burstWindow = ref(props.alert?.burstWindow ?? 0);
const burstPriority = ref(props.alert?.burstPriority ?? 0);

// watchdog / coupled messages
const pairAlertEnabled = ref(
  !!(
    (props.alert?.watchdogWindow && props.alert.watchdogWindow > 0) ||
    props.alert?.watchdogPattern ||
    props.alert?.watchdogTriggerMessage ||
    props.alert?.watchdogClearMessage
  ),
);
const watchdogPattern = ref(props.alert?.watchdogPattern ?? "");
const watchdogWindowMins = ref(props.alert?.watchdogWindow ? Math.round(props.alert.watchdogWindow / 60) : 0);
const watchdogCooldownMins = ref(props.alert?.watchdogCooldown ? Math.round(props.alert.watchdogCooldown / 60) : 0);
const watchdogTriggerMessage = ref(props.alert?.watchdogTriggerMessage ?? "");
const watchdogClearMessage = ref(props.alert?.watchdogClearMessage ?? "");

// per-alert quiet hours override
const alertQuietEnabled = ref(props.alert?.alertQuietEnabled ?? false);
const alertQuietStart = ref(props.alert?.alertQuietStart ?? "22:00");
const alertQuietEnd = ref(props.alert?.alertQuietEnd ?? "07:00");
const alertQuietTimezone = ref(props.alert?.alertQuietTimezone ?? "");

const ntfyFields = computed(() => ({
  ntfyTopic: ntfyTopic.value.trim() || undefined,
  ntfyPriority: ntfyPriority.value || undefined,
  ntfyTags: ntfyTagsInput.value.trim()
    ? ntfyTagsInput.value
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean)
    : undefined,
  bypassQuietHours: bypassQuietHours.value || undefined,
  quietPriority: quietPriority.value || undefined,
  holdDuringQuiet: holdDuringQuiet.value || undefined,
  holdClearWindow: holdClearWindow.value || undefined,
  burstCount: burstCount.value || undefined,
  burstWindow: burstWindow.value || undefined,
  burstPriority: burstPriority.value || undefined,
}));

const hasValidPairAlert = computed(() => {
  if (alertType.value !== "log" || !pairAlertEnabled.value || watchdogWindowMins.value <= 0) return true;
  const triggerExpression = fieldsRef.value?.typeFields.logExpression;
  return Boolean(triggerExpression?.trim() && watchdogPattern.value.trim());
});
const canSave = computed(() => baseCanSave.value && (fieldsRef.value?.canSave ?? false) && hasValidPairAlert.value);

async function save() {
  if (!canSave.value || !fieldsRef.value) return;
  const extra = selectedDestination.value?.type === "ntfy" ? ntfyFields.value : {};
  const watchdog =
    alertType.value === "log" && pairAlertEnabled.value && watchdogWindowMins.value > 0
      ? {
          watchdogPattern: watchdogPattern.value.trim() || undefined,
          watchdogWindow: watchdogWindowMins.value * 60,
          watchdogCooldown: watchdogCooldownMins.value > 0 ? watchdogCooldownMins.value * 60 : undefined,
          watchdogTriggerMessage: watchdogTriggerMessage.value.trim() || undefined,
          watchdogClearMessage: watchdogClearMessage.value.trim() || undefined,
        }
      : {};
  const alertQuiet = alertQuietEnabled.value
    ? {
        alertQuietEnabled: true,
        alertQuietStart: alertQuietStart.value,
        alertQuietEnd: alertQuietEnd.value,
        alertQuietTimezone: alertQuietTimezone.value.trim() || undefined,
      }
    : { alertQuietEnabled: false };
  await saveAlert({ ...fieldsRef.value.typeFields, ...extra, ...watchdog, ...alertQuiet });
}

// Container editor
setupContainerEditor(containerEditorRef);
</script>
