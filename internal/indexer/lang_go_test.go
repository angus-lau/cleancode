package indexer

import (
	"context"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

func parseGo(t *testing.T, source string) *sitter.Node {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(source))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Cleanup(func() { tree.Close() })
	return tree.RootNode()
}

// --- Symbol extraction ---

func TestGoExtractSymbols_Function(t *testing.T) {
	source := `package main

func Hello(name string) string {
    return "hello " + name
}

func add(a, b int) int {
    return a + b
}`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "Hello", Function)
	assertSymbol(t, symbols, "add", Function)

	// Hello is exported, add is not
	for _, s := range symbols {
		if s.Name == "Hello" && s.ExportName == "" {
			t.Error("Hello should be exported")
		}
		if s.Name == "add" && s.ExportName != "" {
			t.Error("add should not be exported")
		}
	}
}

func TestGoExtractSymbols_Method(t *testing.T) {
	source := `package main

type UserService struct{}

func (s UserService) GetUser(id string) string {
    return id
}

func (s UserService) deleteUser(id string) {
}`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "UserService", Class) // struct -> Class
	assertSymbol(t, symbols, "UserService.GetUser", Method)
	assertSymbol(t, symbols, "UserService.deleteUser", Method)

	// GetUser is exported, deleteUser is not
	for _, s := range symbols {
		if s.Name == "UserService.GetUser" && s.ExportName == "" {
			t.Error("UserService.GetUser should be exported")
		}
		if s.Name == "UserService.deleteUser" && s.ExportName != "" {
			t.Error("UserService.deleteUser should not be exported")
		}
	}
}

func TestGoExtractSymbols_PointerReceiver(t *testing.T) {
	source := `package main

type Cache struct{}

func (c *Cache) Get(key string) string {
    return ""
}

func (c *Cache) Set(key, value string) {
}`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "Cache", Class)
	assertSymbol(t, symbols, "Cache.Get", Method)
	assertSymbol(t, symbols, "Cache.Set", Method)
}

func TestGoExtractSymbols_Struct(t *testing.T) {
	source := `package main

type User struct {
    ID    string
    Name  string
    Email string
}

type config struct {
    host string
    port int
}`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "User", Class)
	assertSymbol(t, symbols, "config", Class)

	// User is exported, config is not
	for _, s := range symbols {
		if s.Name == "User" && s.ExportName == "" {
			t.Error("User should be exported")
		}
		if s.Name == "config" && s.ExportName != "" {
			t.Error("config should not be exported")
		}
	}
}

func TestGoExtractSymbols_Interface(t *testing.T) {
	source := `package main

type Reader interface {
    Read(p []byte) (n int, err error)
}

type ReadWriter interface {
    Read(p []byte) (n int, err error)
    Write(p []byte) (n int, err error)
}`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "Reader", Interface)
	assertSymbol(t, symbols, "ReadWriter", Interface)
}

func TestGoExtractSymbols_TypeAlias(t *testing.T) {
	source := `package main

type UserID string
type Score float64
type Handler func(req Request) Response`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "UserID", TypeAlias)
	assertSymbol(t, symbols, "Score", TypeAlias)
	assertSymbol(t, symbols, "Handler", TypeAlias)
}

func TestGoExtractSymbols_VarDeclaration(t *testing.T) {
	source := `package main

var GlobalDB *Database
var version = "1.0.0"
var ErrNotFound = "not found"`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "GlobalDB", Variable)
	assertSymbol(t, symbols, "version", Variable)
	assertSymbol(t, symbols, "ErrNotFound", Variable)

	// GlobalDB and ErrNotFound are exported, version is not
	for _, s := range symbols {
		if s.Name == "GlobalDB" && s.ExportName == "" {
			t.Error("GlobalDB should be exported")
		}
		if s.Name == "ErrNotFound" && s.ExportName == "" {
			t.Error("ErrNotFound should be exported")
		}
		if s.Name == "version" && s.ExportName != "" {
			t.Error("version should not be exported")
		}
	}
}

func TestGoExtractSymbols_ConstDeclaration(t *testing.T) {
	source := `package main

const MaxRetries = 3
const defaultTimeout = 30

const (
    StatusActive  = "active"
    StatusPending = "pending"
)`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	assertSymbol(t, symbols, "MaxRetries", Variable)
	assertSymbol(t, symbols, "defaultTimeout", Variable)
	assertSymbol(t, symbols, "StatusActive", Variable)
	assertSymbol(t, symbols, "StatusPending", Variable)
}

func TestGoExtractSymbols_ExportedVsUnexported(t *testing.T) {
	source := `package main

func PublicFunc() {}
func privateFunc() {}

type PublicStruct struct{}
type privateStruct struct{}

type PublicInterface interface{}
type privateInterface interface{}

var PublicVar = 1
var privateVar = 2

const PublicConst = "pub"
const privateConst = "priv"`
	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	exported := map[string]bool{
		"PublicFunc":      true,
		"PublicStruct":    true,
		"PublicInterface": true,
		"PublicVar":       true,
		"PublicConst":     true,
	}
	unexported := map[string]bool{
		"privateFunc":      true,
		"privateStruct":    true,
		"privateInterface": true,
		"privateVar":       true,
		"privateConst":     true,
	}

	for _, s := range symbols {
		if exported[s.Name] && s.ExportName == "" {
			t.Errorf("%q should be exported", s.Name)
		}
		if unexported[s.Name] && s.ExportName != "" {
			t.Errorf("%q should not be exported", s.Name)
		}
	}
}

// --- Import extraction ---

func TestGoExtractImports_Single(t *testing.T) {
	source := `package main

import "fmt"`
	h := &GoHandler{}
	root := parseGo(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}

	if imports[0].Source != "fmt" {
		t.Errorf("expected source %q, got %q", "fmt", imports[0].Source)
	}
	if len(imports[0].Specifiers) != 1 || imports[0].Specifiers[0] != "fmt" {
		t.Errorf("expected specifier [fmt], got %v", imports[0].Specifiers)
	}
}

func TestGoExtractImports_Grouped(t *testing.T) {
	source := `package main

import (
    "fmt"
    "os"
    "strings"
)`
	h := &GoHandler{}
	root := parseGo(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(imports))
	}

	expected := []string{"fmt", "os", "strings"}
	for i, imp := range imports {
		if imp.Source != expected[i] {
			t.Errorf("import %d: expected source %q, got %q", i, expected[i], imp.Source)
		}
	}
}

func TestGoExtractImports_ThirdParty(t *testing.T) {
	source := `package main

import (
    "fmt"

    "github.com/spf13/cobra"
    "github.com/mattn/go-sqlite3"
)`
	h := &GoHandler{}
	root := parseGo(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(imports))
	}

	// Third-party imports should use the last path segment as specifier
	for _, imp := range imports {
		switch imp.Source {
		case "github.com/spf13/cobra":
			if len(imp.Specifiers) != 1 || imp.Specifiers[0] != "cobra" {
				t.Errorf("cobra import: expected specifier [cobra], got %v", imp.Specifiers)
			}
		case "github.com/mattn/go-sqlite3":
			if len(imp.Specifiers) != 1 || imp.Specifiers[0] != "go-sqlite3" {
				t.Errorf("sqlite3 import: expected specifier [go-sqlite3], got %v", imp.Specifiers)
			}
		}
	}
}

func TestGoExtractImports_Aliased(t *testing.T) {
	source := `package main

import (
    sitter "github.com/smacker/go-tree-sitter"
    ts "github.com/smacker/go-tree-sitter/typescript/typescript"
)`
	h := &GoHandler{}
	root := parseGo(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	// Aliased imports should use alias as specifier
	for _, imp := range imports {
		switch imp.Source {
		case "github.com/smacker/go-tree-sitter":
			if len(imp.Specifiers) != 1 || imp.Specifiers[0] != "sitter" {
				t.Errorf("sitter import: expected specifier [sitter], got %v", imp.Specifiers)
			}
		case "github.com/smacker/go-tree-sitter/typescript/typescript":
			if len(imp.Specifiers) != 1 || imp.Specifiers[0] != "ts" {
				t.Errorf("ts import: expected specifier [ts], got %v", imp.Specifiers)
			}
		}
	}
}

func TestGoExtractImports_DotImport(t *testing.T) {
	source := `package main

import . "fmt"`
	h := &GoHandler{}
	root := parseGo(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}

	if !imports[0].IsNamespace {
		t.Error("dot import should be IsNamespace=true")
	}
}

func TestGoExtractImports_BlankImport(t *testing.T) {
	source := `package main

import (
    _ "github.com/mattn/go-sqlite3"
    "fmt"
)`
	h := &GoHandler{}
	root := parseGo(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	// Blank import should still have the source path, specifier stays as last segment
	for _, imp := range imports {
		if imp.Source == "github.com/mattn/go-sqlite3" {
			// blank import: alias is "_", so pkgName stays as last segment
			if len(imp.Specifiers) != 1 || imp.Specifiers[0] != "go-sqlite3" {
				t.Errorf("blank import: expected specifier [go-sqlite3], got %v", imp.Specifiers)
			}
			return
		}
	}
	t.Error("sqlite3 blank import not found")
}

// --- Full file ---

func TestGoExtractSymbols_FullFile(t *testing.T) {
	source := `package indexer

import (
    "context"
    "fmt"
    "strings"

    sitter "github.com/smacker/go-tree-sitter"
)

const MaxSymbols = 10000

var ErrParseFailed = fmt.Errorf("parse failed")

type SymbolKind string

type Symbol struct {
    Name     string
    Kind     SymbolKind
    FilePath string
}

type Extractor interface {
    Extract(root *sitter.Node) []Symbol
}

type GoExtractor struct {
    parser *sitter.Parser
}

func NewGoExtractor() *GoExtractor {
    return &GoExtractor{}
}

func (e *GoExtractor) Extract(root *sitter.Node) []Symbol {
    return nil
}

func (e *GoExtractor) parseFile(path string) error {
    return nil
}

func helperFunc(s string) string {
    return strings.TrimSpace(s)
}`

	h := &GoHandler{}
	root := parseGo(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.go")

	if len(symbols) < 9 {
		t.Errorf("expected at least 9 symbols, got %d", len(symbols))
		for _, s := range symbols {
			t.Logf("  %s %s (%s:%d-%d)", s.Kind, s.Name, s.FilePath, s.StartLine, s.EndLine)
		}
	}

	// Const and var
	assertSymbol(t, symbols, "MaxSymbols", Variable)
	assertSymbol(t, symbols, "ErrParseFailed", Variable)

	// Types
	assertSymbol(t, symbols, "SymbolKind", TypeAlias)
	assertSymbol(t, symbols, "Symbol", Class) // struct
	assertSymbol(t, symbols, "Extractor", Interface)
	assertSymbol(t, symbols, "GoExtractor", Class) // struct

	// Functions and methods
	assertSymbol(t, symbols, "NewGoExtractor", Function)
	assertSymbol(t, symbols, "GoExtractor.Extract", Method)
	assertSymbol(t, symbols, "GoExtractor.parseFile", Method)
	assertSymbol(t, symbols, "helperFunc", Function)

	// Verify imports
	imports := h.ExtractImports(root, []byte(source))
	if len(imports) != 4 {
		t.Errorf("expected 4 imports, got %d", len(imports))
	}

	// Verify export status on key symbols
	for _, s := range symbols {
		switch s.Name {
		case "MaxSymbols", "ErrParseFailed", "SymbolKind", "Symbol", "Extractor",
			"GoExtractor", "NewGoExtractor", "GoExtractor.Extract":
			if s.ExportName == "" {
				t.Errorf("%q should be exported", s.Name)
			}
		case "GoExtractor.parseFile", "helperFunc":
			if s.ExportName != "" {
				t.Errorf("%q should not be exported", s.Name)
			}
		}
	}
}
