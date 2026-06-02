const show = ref(false);
export const popupDelayMs = 150;
const debouncedShow = debouncedRef(show, popupDelayMs);

const delayedShow = computed({
  set(newVal: boolean) {
    show.value = newVal;
  },
  get() {
    return debouncedShow.value;
  },
});

export const globalShowPopup = () => delayedShow;
