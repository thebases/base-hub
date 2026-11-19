// bootstrap-guard.js hardens the Bootstrap 5 build bundled inside
// @thebase/ui (published as globalThis.bootstrap when nodexui.esm.js is
// evaluated) against components being disposed mid-transition.
//
// BaseUI's Owl wrappers (DialogContent, Toast, ...) dispose their Bootstrap
// instance on unmount. Any flow that closes a Dialog and then re-renders the
// page (ModelView load() -> state.loading, reloadRoute(), navigate()) unmounts
// the modal while its fade-out is still running. Bootstrap's dispose() nulls
// every own property, but the already-queued transition callback
// (Modal._hideModal) still fires and throws
// "Cannot read properties of null (reading 'style')" as a window error, and
// the body is left with `modal-open` + scrollbar compensation styles.
//
// Guarding here once covers every current and future dialog/offcanvas/toast
// instead of every call site having to wait for hidden.bs.modal.
import "nodexui/owl";

const bootstrap = globalThis.bootstrap;

// guardQueuedCallbacks drops queued transition callbacks whose component was
// disposed before the transition ended (dispose() sets _element to null).
function guardQueuedCallbacks(BaseComponent) {
  const queue = BaseComponent.prototype._queueCallback;
  if (typeof queue !== "function" || queue.__basehubGuarded) return;
  const guarded = function (callback, element, isAnimated = true) {
    return queue.call(this, () => { if (this._element) callback(); }, element, isAnimated);
  };
  guarded.__basehubGuarded = true;
  BaseComponent.prototype._queueCallback = guarded;
}

// finishModalHide performs the body/backdrop cleanup the dropped _hideModal
// callback would have done, so a modal disposed mid-fade doesn't leave the
// page scroll-locked behind an orphaned backdrop.
function finishModalHide(modal) {
  if (modal._isShown || !modal._isTransitioning) return;
  modal._element.style.display = "none";
  modal._element.setAttribute("aria-hidden", "true");
  modal._element.removeAttribute("aria-modal");
  modal._element.removeAttribute("role");
  // Only unlock the body when no other modal is still open.
  if (!document.querySelector(".modal.show")) {
    document.body.classList.remove("modal-open");
    modal._resetAdjustments?.();
    modal._scrollBar?.reset?.();
  }
}

// guardModalDispose completes an in-progress hide before the original
// dispose() nulls the instance (which also removes the backdrop element).
function guardModalDispose(Modal) {
  const dispose = Modal.prototype.dispose;
  if (typeof dispose !== "function" || dispose.__basehubGuarded) return;
  const guarded = function () {
    if (this._element) finishModalHide(this);
    return dispose.call(this);
  };
  guarded.__basehubGuarded = true;
  Modal.prototype.dispose = guarded;
}

if (bootstrap?.Modal) {
  guardQueuedCallbacks(Object.getPrototypeOf(bootstrap.Modal));
  guardModalDispose(bootstrap.Modal);
}
