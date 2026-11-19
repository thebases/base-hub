package main

// File overview:
// - Responsibility: owns the process entrypoint and boot-time handoff into the Cobra command layer.
// - Key symbols: `main`.
// - Pattern: Keeps the entrypoint intentionally small so startup sequencing stays easy to scan while compile-time asset wiring lives beside it in `main_embed.go`.

import (
	"git.thebase.vn/base_hub/internal/cmd" // Import the generated docs
)

func main() {
	cmd.Execute()
}
