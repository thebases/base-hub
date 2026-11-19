// One place for how the canvas reports a failed workflow call: a permission denial (403 / -32003,
// common since viewer/operator/author roles exist) becomes a toast and never an inline error or a
// logout; anything else comes back as the message to show inline.
import { showToast } from "@/modules/base-bus/bus-root.js";

import { workflowErrorInfo } from "@/modules/workflow/api.js";

// reportWorkflowError toasts a permission denial and returns "" (nothing to show inline), or
// returns the server message (or the fallback) for the caller to display.
export function reportWorkflowError(error, fallback) {
  const info = workflowErrorInfo(error);
  if (info.permission) {
    showToast({ message: t("You do not have permission to do this on workflows."), variant: "danger" });
    return "";
  }
  return info.message || fallback;
}

// notifySuccess shows a short success toast.
export function notifySuccess(message) {
  showToast({ message, variant: "success" });
}
