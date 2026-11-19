package apidoc

// File overview:
// - Responsibility: builds API documentation structures for types, translating runtime metadata into discoverable docs and OpenAPI-friendly payloads.
// - Key symbols: `Config`, `IndexDocument`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import "git.thebase.vn/base_hub/internal/orm"

// Config controls how generated API documentation is branded and how endpoint
// URLs are rendered in the OpenAPI document.
type Config struct {
	Title    string
	Version  string
	BasePath string
}

// IndexDocument mirrors the lightweight model catalog used by the
// documentation client for its initial navigation load.
type IndexDocument struct {
	Modules []string     `json:"modules"`
	Models  []IndexModel `json:"models"`
}

type IndexModel struct {
	Model   string                `json:"model"`
	Name    string                `json:"name"`
	Fields  map[string]IndexField `json:"fields"`
	Methods []string              `json:"methods"`
}

type IndexField struct {
	String string `json:"string"`
}

// ModelDocument contains the richer per-model payload used by detail pages and
// OpenAPI generation.
type ModelDocument struct {
	Model string  `json:"model"`
	Name  string  `json:"name"`
	Doc   *string `json:"doc"`
	// DocFile is the markdown body of ModelSpec.DescriptionFile (empty when unset or unreadable).
	DocFile string                    `json:"doc_file,omitempty"`
	Fields  map[string]FieldDocument  `json:"fields"`
	Methods map[string]MethodDocument `json:"methods"`
}

type FieldDocument struct {
	Name       string                `json:"name"`
	String     string                `json:"string"`
	Type       string                `json:"type"`
	Kind       orm.FieldKind         `json:"kind,omitempty"`
	Required   bool                  `json:"required"`
	Readonly   bool                  `json:"readonly"`
	Store      bool                  `json:"store"`
	Searchable bool                  `json:"searchable"`
	Sortable   bool                  `json:"sortable"`
	Help       string                `json:"help,omitempty"`
	Default    string                `json:"default,omitempty"`
	Relation   string                `json:"relation,omitempty"`
	OnDelete   string                `json:"on_delete,omitempty"`
	Module     string                `json:"module,omitempty"`
	Selection  []orm.SelectionOption `json:"selection,omitempty"`
	Related    []string              `json:"related,omitempty"`
	Depends    []string              `json:"depends,omitempty"`
	Compute    string                `json:"compute,omitempty"`
}

type MethodDocument struct {
	Signature      string                  `json:"signature"`
	Parameters     map[string]ParameterDoc `json:"parameters"`
	Return         *ReturnDoc              `json:"return,omitempty"`
	RequestExample any                     `json:"request_example,omitempty"`
	Doc            string                  `json:"doc,omitempty"`
	// DocFile is the markdown body of RuntimeMethod.DescriptionFile; the UI shows a
	// "Show Description" button for it.
	DocFile   string   `json:"doc_file,omitempty"`
	API       []string `json:"api,omitempty"`
	Model     string   `json:"model"`
	Module    string   `json:"module"`
	Public    bool     `json:"public"`
	ReadOnly  bool     `json:"read_only"`
	ModelOnly bool     `json:"model_only"`
	Path      string   `json:"path"`
	// AliasPath is the friendly "/base/<AliasRoute>" transport route mounted
	// for this method, empty when the method declares no alias.
	AliasPath string `json:"alias_path,omitempty"`
	// AliasPaths lists the extra RuntimeMethod.Aliases routes, each as "VERB /path" (e.g.
	// "POST /mqtt/publish"), so the docs page shows every route the method is mounted at.
	AliasPaths []string `json:"alias_paths,omitempty"`
}

type ParameterDoc struct {
	Name       string `json:"name"`
	Annotation string `json:"annotation,omitempty"`
	Default    any    `json:"default,omitempty"`
	Doc        string `json:"doc,omitempty"`
}

type ReturnDoc struct {
	Annotation string `json:"annotation,omitempty"`
	Doc        string `json:"doc,omitempty"`
}
