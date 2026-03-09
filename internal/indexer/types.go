package indexer

type SymbolKind string

const (
	Function  SymbolKind = "function"
	Class     SymbolKind = "class"
	Method    SymbolKind = "method"
	Variable  SymbolKind = "variable"
	TypeAlias SymbolKind = "type"
	Interface SymbolKind = "interface"
	Enum      SymbolKind = "enum"
	Export    SymbolKind = "export"
)

type Symbol struct {
	Name       string     `json:"name"`
	Kind       SymbolKind `json:"kind"`
	FilePath   string     `json:"filePath"`
	StartLine  int        `json:"startLine"`
	EndLine    int        `json:"endLine"`
	ExportName string     `json:"exportName,omitempty"`
}

type ImportRef struct {
	Source      string   `json:"source"`
	Specifiers  []string `json:"specifiers"`
	IsDefault   bool     `json:"isDefault"`
	IsNamespace bool     `json:"isNamespace"`
	ResolvedPath string  `json:"resolvedPath,omitempty"`
}

type FileNode struct {
	Path         string      `json:"path"`
	Symbols      []Symbol    `json:"symbols"`
	Imports      []ImportRef `json:"imports"`
	LastModified int64       `json:"lastModified"`
	Hash         string      `json:"hash"`
}

type Edge struct {
	From string `json:"from"` // symbolID = "filePath:name"
	To   string `json:"to"`
	Type string `json:"type"` // "calls", "imports", "extends", "implements"
}

type CallerResult struct {
	Symbol   Symbol `json:"symbol"`
	CallLine int    `json:"callLine"`
}

type DependentResult struct {
	FilePath string   `json:"filePath"`
	Imports  []string `json:"imports"`
}

type IndexStats struct {
	Files   int `json:"files"`
	Symbols int `json:"symbols"`
	Edges   int `json:"edges"`
}
