// Straight port of frontend-admin's modules/base-bus/event-bus.ts — a small
// pub/sub wrapper over the standard browser EventTarget, with types dropped
// (plain JS here). No framework dependency, safe to use outside Owl/React.

// createBusEventTarget provides the small EventBus surface used by the bus
// service without pulling in legacy OWL runtime.
export function createBusEventTarget() {
  const target = new EventTarget();

  return {
    addEventListener(type, listener) {
      target.addEventListener(type, listener);
    },
    removeEventListener(type, listener) {
      target.removeEventListener(type, listener);
    },
    trigger(type, detail) {
      target.dispatchEvent(new CustomEvent(type, { detail }));
    },
  };
}
