// The canvas's rarely used panels, loaded with one dynamic import after the canvas is idle (or on
// first use) so opening a workflow does not download them: run history, test-run dialog,
// templates gallery and trigger list.
export { RunInspector } from "@/modules/workflow/components/RunInspector.js";
export { TemplatesDialog } from "@/modules/workflow/components/TemplatesDialog.js";
export { TestRunDialog } from "@/modules/workflow/components/TestRunDialog.js";
export { TriggerListPanel } from "@/modules/workflow/components/TriggerListPanel.js";
