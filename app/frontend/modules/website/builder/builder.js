import { PluginManager } from "./core/plugin_manager.js";
import { CanvasPlugin } from "./plugins/canvas_plugin.js";
import { SelectionPlugin } from "./plugins/selection_plugin.js";
import { BlocksPlugin } from "./plugins/blocks_plugin.js";
import { RemovePlugin } from "./plugins/remove_plugin.js";
import { TextPlugin } from "./plugins/text_plugin.js";
import { OverlayPlugin } from "./plugins/overlay_plugin.js";
import { MediaPlugin } from "./plugins/media_plugin.js";
import { OptionsPlugin } from "./plugins/options_plugin.js";
import { ChromePlugin } from "./plugins/chrome_plugin.js";

const CORE_PLUGINS = [CanvasPlugin, SelectionPlugin, BlocksPlugin, RemovePlugin, TextPlugin, OverlayPlugin, MediaPlugin, OptionsPlugin, ChromePlugin];

// Builds the plugin graph around a tiny event bus shared as `ctx`.
export function createBuilder(extraPlugins = []) {
  const listeners = new Map();
  const ctx = {
    on(name, fn) {
      if (!listeners.has(name)) listeners.set(name, new Set());
      listeners.get(name).add(fn);
    },
    emit(name, payload) {
      listeners.get(name)?.forEach((fn) => fn(payload));
    },
  };
  ctx.plugins = new PluginManager(ctx, [...CORE_PLUGINS, ...extraPlugins]);
  ctx.plugins.start();
  return ctx;
}
