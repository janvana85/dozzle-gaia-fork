const { hostname, appName } = config;
let subtitle = $ref("");
const title = $computed(() => (subtitle ? `${subtitle} - ` : "") + appName + (hostname ? ` @ ${hostname}` : ""));

useTitle($$(title));

export function setTitle(t: string) {
  subtitle = t;
}
