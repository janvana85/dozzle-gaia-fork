<template>
  <div class="space-y-4">
    <!-- Name -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.destination-form.name") }}</legend>
      <input
        ref="nameInput"
        v-model="name"
        type="text"
        class="input focus:input-primary w-full text-base"
        :class="{ 'input-primary': name.trim().length > 0 }"
        required
        :placeholder="$t('notifications.destination-form.name-placeholder')"
      />
    </fieldset>

    <!-- Server URL -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.destination-form.ntfy-server-url") }}</legend>
      <input
        v-model="serverUrl"
        type="url"
        class="input focus:input-primary w-full text-base"
        :class="{ 'input-primary': isValidUrl, 'input-error': serverUrl.trim() && !isValidUrl }"
        :placeholder="$t('notifications.destination-form.ntfy-server-url-placeholder')"
      />
    </fieldset>

    <!-- Default Topic -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.destination-form.ntfy-topic") }}</legend>
      <input
        v-model="topic"
        type="text"
        class="input focus:input-primary w-full text-base"
        :class="{ 'input-primary': topic.trim().length > 0 }"
        :placeholder="$t('notifications.destination-form.ntfy-topic-placeholder')"
      />
    </fieldset>

    <!-- Default Priority -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.destination-form.ntfy-priority") }}</legend>
      <select v-model="priority" class="select focus:select-primary w-full text-base">
        <option :value="1">{{ $t("notifications.destination-form.ntfy-priority-1") }}</option>
        <option :value="2">{{ $t("notifications.destination-form.ntfy-priority-2") }}</option>
        <option :value="3">{{ $t("notifications.destination-form.ntfy-priority-3") }}</option>
        <option :value="4">{{ $t("notifications.destination-form.ntfy-priority-4") }}</option>
        <option :value="5">{{ $t("notifications.destination-form.ntfy-priority-5") }}</option>
      </select>
    </fieldset>

    <!-- Auth Token (optional) -->
    <fieldset class="fieldset">
      <legend class="fieldset-legend text-lg">{{ $t("notifications.destination-form.ntfy-token") }}</legend>
      <input
        v-model="token"
        type="password"
        class="input focus:input-primary w-full text-base"
        :placeholder="destination?.tokenSet ? $t('notifications.destination-form.ntfy-token-set') : 'tk_...'"
        autocomplete="new-password"
      />
      <p v-if="destination?.tokenSet && !token" class="fieldset-label text-base-content/50 text-xs">
        {{ $t("notifications.destination-form.ntfy-token-keep") }}
      </p>
    </fieldset>

    <!-- Error -->
    <div v-if="error" class="alert alert-error">
      <span>{{ error }}</span>
    </div>

    <!-- Test Result -->
    <div v-if="testResult" class="alert" :class="testResult.success ? 'alert-success' : 'alert-error'">
      <span v-if="testResult.success">{{ $t("notifications.destination-form.test-success") }}</span>
      <span v-else>{{ testResult.error }}</span>
    </div>

    <!-- Actions -->
    <div class="flex items-center gap-2 pt-4">
      <button class="btn" :disabled="!canTest || isTesting" @click="testDestination">
        <span v-if="isTesting" class="loading loading-spinner loading-sm"></span>
        {{ $t("notifications.destination-form.test") }}
      </button>
      <div class="flex-1"></div>
      <button class="btn" @click="close?.()">{{ $t("notifications.destination-form.cancel") }}</button>
      <button class="btn btn-primary" :disabled="!canSave" @click="saveDestination">
        <span v-if="isSaving" class="loading loading-spinner loading-sm"></span>
        {{ isEditing ? $t("notifications.destination-form.save") : $t("notifications.destination-form.add") }}
      </button>
    </div>
  </div>
</template>

<script lang="ts" setup>
import type { Dispatcher, TestWebhookResult } from "@/types/notifications";

const { close, onCreated, destination, isEditing } = defineProps<{
  close?: () => void;
  onCreated?: () => void;
  destination?: Dispatcher;
  isEditing: boolean;
}>();

const nameInput = ref<HTMLInputElement>();
useFocus(nameInput, { initialValue: true });

const name = ref(destination?.name ?? "");
const serverUrl = ref(destination?.url ?? "https://ntfy.sh");
const topic = ref(destination?.topic ?? "");
const priority = ref(destination?.priority ?? 3);
const token = ref(""); // always starts empty; backend preserves existing token if left blank

const isTesting = ref(false);
const isSaving = ref(false);
const error = ref<string | null>(null);
const testResult = ref<TestWebhookResult | null>(null);

const isValidUrl = computed(() => {
  try {
    new URL(serverUrl.value.trim());
    return true;
  } catch {
    return false;
  }
});

const canTest = computed(() => isValidUrl.value && topic.value.trim().length > 0);

const canSave = computed(
  () => !isSaving.value && name.value.trim().length > 0 && isValidUrl.value && topic.value.trim().length > 0,
);

async function testDestination() {
  if (!canTest.value) return;
  isTesting.value = true;
  testResult.value = null;
  try {
    const body: Record<string, unknown> = {
      url: serverUrl.value.trim(),
      topic: topic.value.trim(),
      priority: priority.value,
    };
    if (token.value.trim()) {
      body.token = token.value.trim();
    } else if (isEditing && destination?.id) {
      // pass dispatcher ID so backend can use stored token
      body.dispatcherId = destination.id;
    }
    const res = await fetch(withBase("/api/notifications/test-ntfy"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data: TestWebhookResult = await res.json();
    testResult.value = data;
  } catch (e) {
    testResult.value = { success: false, error: e instanceof Error ? e.message : "Test failed" };
  } finally {
    isTesting.value = false;
  }
}

async function saveDestination() {
  if (!canSave.value) return;
  isSaving.value = true;
  error.value = null;
  try {
    const input: Record<string, unknown> = {
      name: name.value.trim(),
      type: "ntfy",
      url: serverUrl.value.trim(),
      topic: topic.value.trim(),
      priority: priority.value,
    };
    // Only include token if the user typed something; backend preserves the existing token otherwise
    if (token.value.trim()) {
      input.token = token.value.trim();
    }
    const endpoint = isEditing
      ? withBase(`/api/notifications/dispatchers/${destination!.id}`)
      : withBase("/api/notifications/dispatchers");
    const res = await fetch(endpoint, {
      method: isEditing ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    if (!res.ok) {
      const data = await res.json();
      throw new Error(data.error || "Failed to save destination");
    }
    onCreated?.();
    close?.();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to save destination";
  } finally {
    isSaving.value = false;
  }
}
</script>
