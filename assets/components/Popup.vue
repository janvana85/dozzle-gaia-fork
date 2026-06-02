<template>
  <span class="contents" @mouseenter="onMouseEnter" @mouseleave="onMouseLeave">
    <slot></slot>
  </span>
  <teleport to="body">
    <transition name="fade">
      <div
        v-show="show && (delayedShow || globalShow)"
        class="ring-base-content/20 bg-base-100 pointer-events-none fixed z-50 rounded-sm p-3 shadow-sm ring"
        ref="content"
      >
        <slot name="content"></slot>
      </div>
    </transition>
  </teleport>
</template>

<script lang="ts" setup>
import { globalShowPopup, popupDelayMs } from "@/composable/popup";

const globalShow = globalShowPopup();
const show = ref(globalShow.value);
const delayedShow = refDebounced(show, popupDelayMs);
const content = ref<HTMLElement>();

const onMouseEnter = (e: MouseEvent) => {
  show.value = true;
  globalShow.value = true;

  if (content.value && e.currentTarget instanceof HTMLElement) {
    const { left, top, width } = e.currentTarget.getBoundingClientRect();
    const x = left + width + 10;
    const y = top;

    content.value.style.left = `${x}px`;
    content.value.style.top = `${y}px`;
  }
};

const onMouseLeave = () => {
  show.value = false;
  globalShow.value = false;
};
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  @apply transition-opacity;
}

.fade-enter-from,
.fade-leave-to {
  @apply opacity-0;
}
</style>
