import { type Settings } from "@/stores/settings";
import { Host } from "@/stores/hosts";

const text = document.querySelector("script#config__json")?.textContent || "{}";

export interface Config {
  version: string;
  base: string;
  maxLogs: number;
  appName: string;
  hostname: string;
  mode: "server" | "swarm" | "k8s";
  hosts: Host[];
  authProvider: "simple" | "none" | "forward-proxy";
  logoutUrl?: string;
  enableActions: boolean;
  enableShell: boolean;
  enableDownload: boolean;
  disableAvatars: boolean;
  releaseCheckMode: "automatic" | "manual";
  user?: {
    username: string;
    email: string;
    name: string;
  };
  profile?: Profile;
}

export interface Profile {
  settings?: Settings;
  pinned?: Set<string>;
  visibleKeys?: Map<string, Map<string[], boolean>>;
  releaseSeen?: string;
  collapsedGroups?: Set<string>;
  collapsedHostGroups?: Set<string>;
  cloudWelcomeShown?: boolean;
}

const pageConfig = JSON.parse(text);

const config: Config = {
  maxLogs: 400,
  version: "v0.0.0",
  hosts: [],
  appName: "Dozzle Gaia",
  ...pageConfig,
};

export default Object.freeze(config);

function normalizeBase(path: string): string {
  if (!path || path === "/") {
    return "";
  }
  const prefixed = path.startsWith("/") ? path : `/${path}`;
  return prefixed.length > 1 ? prefixed.replace(/\/+$/, "") : prefixed;
}

const base = normalizeBase(config.base);
export const withBase = (path: string) => `${base}${path.startsWith("/") ? path : `/${path}`}`;
