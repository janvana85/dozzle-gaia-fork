<template>
  <ScrollableView :scrollable="scrollable" v-if="host">
    <template #header>
      <div class="mx-2 flex items-center gap-2 md:ml-4">
        <div class="flex flex-1 items-center gap-1.5 truncate md:gap-2">
          <ph:computer-tower />
          <div class="inline-flex font-mono text-sm">
            <div class="font-semibold">{{ host.name }}</div>
          </div>
          <Tag v-if="!host.available" class="font-mono" size="small" type="warning">offline + cached</Tag>
          <Tag class="font-mono max-md:hidden" size="small">
            {{ $t("label.container", containers.length) }}
          </Tag>
        </div>
        <MultiContainerStat class="ml-auto" :containers="containers" />
        <MultiContainerActionToolbar class="max-md:hidden" :name="host.name" @clear="viewer?.clear()" />
      </div>
    </template>
    <template #default>
      <div v-if="showOfflineNoCacheMessage" class="text-base-content/70 p-4 text-sm">
        {{ $t("label.no-cached-logs-yet") }}
      </div>
      <ViewerWithSource
        v-else
        ref="viewer"
        :stream-source="useHostStream"
        :entity="host"
        :visible-keys="new Map<string[], boolean>()"
      />
    </template>
  </ScrollableView>
</template>

<script lang="ts" setup>
import ViewerWithSource from "@/components/LogViewer/ViewerWithSource.vue";
import { ComponentExposed } from "vue-component-type-helpers";
const { id, scrollable = false } = defineProps<{
  id: string;
  scrollable?: boolean;
}>();
const store = useContainerStore();
const { containersByHost } = storeToRefs(store);
const { hosts } = useHosts();
const host = computed(() => hosts.value[id]);
const containers = computed(() => {
  const items = containersByHost.value?.[id] ?? [];
  if (host.value?.available) return items.filter((c) => c.state === "running");
  return items.filter((c) => c.state !== "deleted");
});
const showOfflineNoCacheMessage = computed(() =>
  Boolean(host.value && !host.value.available && containers.value.length === 0),
);
const viewer = useTemplateRef<ComponentExposed<typeof ViewerWithSource>>("viewer");
provideLoggingContext(containers, { showContainerName: true, showHostname: false });
</script>
