<template>
  <div class="join">
    <select
      class="select select-sm join-item focus:select-primary w-20"
      :value="displayHour"
      :aria-label="label ? `${label} hour` : 'Hour'"
      @change="setHour(($event.target as HTMLSelectElement).value)"
    >
      <option v-for="hour in hourOptions" :key="hour" :value="hour">{{ hour }}</option>
    </select>
    <select
      class="select select-sm join-item focus:select-primary w-20"
      :value="minute"
      :aria-label="label ? `${label} minute` : 'Minute'"
      @change="setMinute(($event.target as HTMLSelectElement).value)"
    >
      <option v-for="option in minuteOptions" :key="option" :value="option">{{ option }}</option>
    </select>
    <select
      v-if="usesTwelveHourClock"
      class="select select-sm join-item focus:select-primary w-20"
      :value="period"
      :aria-label="label ? `${label} period` : 'Period'"
      @change="setPeriod(($event.target as HTMLSelectElement).value as Period)"
    >
      <option value="AM">AM</option>
      <option value="PM">PM</option>
    </select>
  </div>
</template>

<script lang="ts" setup>
type Period = "AM" | "PM";

const props = defineProps<{
  modelValue: string;
  label?: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
  change: [value: string];
}>();

const minuteOptions = Array.from({ length: 60 }, (_, value) => String(value).padStart(2, "0"));

const usesTwelveHourClock = computed(() => {
  if (hourStyle.value === "12") return true;
  if (hourStyle.value === "24") return false;

  const hourCycle = new Intl.DateTimeFormat(undefined, { hour: "numeric" }).resolvedOptions().hourCycle;
  return hourCycle === "h11" || hourCycle === "h12";
});

const hourOptions = computed(() => {
  const length = usesTwelveHourClock.value ? 12 : 24;
  return Array.from({ length }, (_, index) => {
    const hour = usesTwelveHourClock.value ? index + 1 : index;
    return String(hour).padStart(2, "0");
  });
});

const parsedTime = computed(() => {
  const match = props.modelValue.match(/^(\d{1,2}):(\d{2})$/);
  if (!match) return { hour: 0, minute: "00" };

  const hour = clamp(Number.parseInt(match[1], 10), 0, 23);
  const minute = clamp(Number.parseInt(match[2], 10), 0, 59);
  return { hour, minute: String(minute).padStart(2, "0") };
});

const minute = computed(() => parsedTime.value.minute);
const period = computed<Period>(() => (parsedTime.value.hour >= 12 ? "PM" : "AM"));
const displayHour = computed(() => {
  if (!usesTwelveHourClock.value) return String(parsedTime.value.hour).padStart(2, "0");

  const hour = parsedTime.value.hour % 12 || 12;
  return String(hour).padStart(2, "0");
});

function setHour(value: string) {
  const selectedHour = Number.parseInt(value, 10);
  const nextHour = usesTwelveHourClock.value ? toTwentyFourHour(selectedHour, period.value) : selectedHour;
  update(nextHour, minute.value);
}

function setMinute(value: string) {
  update(parsedTime.value.hour, value);
}

function setPeriod(value: Period) {
  update(toTwentyFourHour(Number.parseInt(displayHour.value, 10), value), minute.value);
}

function update(hour: number, nextMinute: string) {
  const value = `${String(clamp(hour, 0, 23)).padStart(2, "0")}:${nextMinute}`;
  emit("update:modelValue", value);
  emit("change", value);
}

function toTwentyFourHour(hour: number, nextPeriod: Period) {
  const normalized = hour % 12;
  return nextPeriod === "PM" ? normalized + 12 : normalized;
}

function clamp(value: number, min: number, max: number) {
  if (Number.isNaN(value)) return min;
  return Math.min(max, Math.max(min, value));
}
</script>
