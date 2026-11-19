// Operation + history. Every content change runs through `operation.run(label, fn)`.
// History is snapshot based: the canvas body root is serialized before and after the operation, and
// undo/redo restore a snapshot. Snapshots are small (one page) and make undo independent of how a
// plugin mutated the DOM. `root` is any object with `get(): string` and `set(html: string)`.

export class History {
  constructor(root, { limit = 100 } = {}) {
    this.root = root;
    this.limit = limit;
    this.undoStack = [];
    this.redoStack = [];
    this.listeners = new Set();
    this.beforeChange = new Set();
    this.busy = false;
    this.savedSnapshot = root.get();
  }
  on(fn) {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }
  // Hooks run before any run/undo/redo starts, so pending continuous input (typing) can be committed
  // first and never lands out of order in the stacks.
  onBeforeChange(fn) {
    this.beforeChange.add(fn);
  }
  prepare() {
    for (const fn of this.beforeChange) fn();
  }
  emit() {
    for (const fn of this.listeners) fn(this.state());
  }
  state() {
    return { canUndo: this.undoStack.length > 0, canRedo: this.redoStack.length > 0, dirty: this.root.get() !== this.savedSnapshot };
  }
  // Runs fn as one undoable step. Nested runs are folded into the outer step. A step that leaves the
  // content unchanged is not recorded. If fn throws, the content is restored and the error rethrown.
  async run(label, fn) {
    if (this.busy) return fn();
    this.prepare();
    this.busy = true;
    const before = this.root.get();
    try {
      const result = await fn();
      const after = this.root.get();
      if (after !== before) {
        this.undoStack.push({ label, before, after });
        if (this.undoStack.length > this.limit) this.undoStack.shift();
        this.redoStack = [];
      }
      return result;
    } catch (error) {
      this.root.set(before);
      throw error;
    } finally {
      this.busy = false;
      this.emit();
    }
  }
  // Records a step for a change that already happened (continuous input such as typing), given the
  // snapshot taken before it started. No-op when the content is unchanged.
  commit(label, before) {
    if (this.busy) return;
    const after = this.root.get();
    if (after !== before) {
      this.undoStack.push({ label, before, after });
      if (this.undoStack.length > this.limit) this.undoStack.shift();
      this.redoStack = [];
    }
    // Always tell listeners: the content may differ from the saved snapshot even when this step
    // recorded nothing (the "before" snapshot was taken after the first keystroke).
    this.emit();
  }
  undo() {
    this.prepare();
    const step = this.undoStack.pop();
    if (!step) return false;
    this.root.set(step.before);
    this.redoStack.push(step);
    this.emit();
    return true;
  }
  redo() {
    this.prepare();
    const step = this.redoStack.pop();
    if (!step) return false;
    this.root.set(step.after);
    this.undoStack.push(step);
    this.emit();
    return true;
  }
  // `snapshot` is the content that was actually sent to the server; defaults to the current content.
  markSaved(snapshot = this.root.get()) {
    this.savedSnapshot = snapshot;
    this.emit();
  }
  // Discards unsaved work: restores the last saved snapshot and clears the stacks.
  discard() {
    this.root.set(this.savedSnapshot);
    this.undoStack = [];
    this.redoStack = [];
    this.emit();
  }
}
