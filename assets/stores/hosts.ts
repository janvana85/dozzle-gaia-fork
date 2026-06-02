export type Host = {
  id: string;
  name: string;
  nCPU: number;
  memTotal: number;
  type: "agent" | "local" | "remote" | "swarm" | "k8s";
  endpoint: string;
  available: boolean;
  dockerVersion: string;
  runtime?: "docker" | "podman";
  agentVersion: string;
  group?: string;
};

const hosts = ref(
  config.hosts
    .sort((a, b) => a.name.localeCompare(b.name))
    .reduce(
      (acc, item) => {
        acc[item.id] = item;
        return acc;
      },
      {} as Record<string, Host>,
    ),
);
const updateHost = (host: Host) => {
  delete hosts.value[host.id];
  for (const [key, value] of Object.entries(hosts.value)) {
    if (value.endpoint === host.endpoint) {
      delete hosts.value[key];
    }
  }
  hosts.value[host.id] = host;
  return host;
};

export function useHosts() {
  return {
    hosts,
    updateHost,
  };
}
