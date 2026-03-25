// Compatibility shim for openclaw/plugin-sdk/plugin-entry.
// Allows OpenClaw plugins using definePluginEntry to run in gostaff.

export function definePluginEntry(opts) {
  const configSchema =
    typeof opts.configSchema === "function"
      ? opts.configSchema()
      : opts.configSchema || {};
  return {
    id: opts.id,
    name: opts.name,
    description: opts.description || "",
    kind: opts.kind,
    configSchema,
    register: opts.register,
  };
}

export function defineChannelPluginEntry(opts) {
  return definePluginEntry({
    ...opts,
    register(api) {
      if (opts.channel) api.registerChannel(opts.channel);
      if (opts.register) opts.register(api);
    },
  });
}

export function defineSetupPluginEntry(plugin) {
  return { plugin };
}
