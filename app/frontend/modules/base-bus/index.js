// Barrel export mirroring frontend-admin's modules/base-bus/index.ts public
// surface, minus type-only exports (dropped — plain JS here) and minus
// `BusProvider` (React-specific; this tree's equivalent is bus-root.js's
// `initBusRoot`/`subscribeBusToast`, exported separately since it has no
// component to export — see bus-root.js's header comment for why).
export { busService, createBusService } from "./bus-service.js";
export {
  dispatchActionClient,
  registerActionClientHandler,
  unregisterActionClientHandler,
} from "./action-client.js";
export { initBusRoot, subscribeBusToast } from "./bus-root.js";
