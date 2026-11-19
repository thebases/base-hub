package etlflow

// File overview:
// - Responsibility: encapsulates domain/service orchestration for action hooks, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: package init hooks and file-local helpers.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.
