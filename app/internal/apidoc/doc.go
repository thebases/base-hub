// Package apidoc builds API documentation from the runtime model
// registry without coupling the transport layer to one specific frontend.
package apidoc

// File overview:
// - Responsibility: builds API documentation structures for doc, translating runtime metadata into discoverable docs and OpenAPI-friendly payloads.
// - Key symbols: package init hooks and file-local helpers.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.
