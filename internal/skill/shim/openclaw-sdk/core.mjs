// Compatibility shim for openclaw/plugin-sdk/core.
// Re-exports plugin-entry helpers + stubs for utilities.

export {
  definePluginEntry,
  defineChannelPluginEntry,
  defineSetupPluginEntry,
} from "./plugin-entry.mjs";

export const emptyPluginConfigSchema = {};

export function normalizeAccountId(id) {
  return id || "default";
}
export const DEFAULT_ACCOUNT_ID = "default";

export function normalizeAtHashSlug(s) {
  return s.replace(/[^a-z0-9-]/gi, "-").toLowerCase();
}
export function normalizeHyphenSlug(s) {
  return s.replace(/[^a-z0-9-]/gi, "-").toLowerCase();
}

export function isSecretRef(v) {
  return typeof v === "string" && v.startsWith("secret:");
}

export function loadSecretFileSync() { return ""; }
export function readSecretFileSync() { return ""; }
export function tryReadSecretFileSync() { return undefined; }

export function resolveGatewayBindUrl() { return "http://localhost:0"; }
export function resolveGatewayPort() { return 0; }

export function buildAgentSessionKey(parts) { return parts.join(":"); }
export function resolveThreadSessionKeys() { return []; }

export function stripChannelTargetPrefix(s) { return s; }
export function stripTargetKindPrefix(s) { return s; }
export function buildChannelOutboundSessionRoute() { return ""; }

export function delegateCompactionToRuntime() { return null; }

export class KeyedAsyncQueue {
  async enqueue(_key, fn) { return fn(); }
}
export function enqueueKeyedTask(_queue, _key, fn) { return fn(); }
