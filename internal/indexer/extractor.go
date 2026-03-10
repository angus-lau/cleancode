package indexer

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/swift"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// LangHandler extracts symbols and imports from a parsed AST.
type LangHandler interface {
	ExtractSymbols(root *sitter.Node, source []byte, filePath string) []Symbol
	ExtractImports(root *sitter.Node, source []byte) []ImportRef
}

type langEntry struct {
	parser  *sitter.Parser
	handler LangHandler
}

type Extractor struct {
	langs map[string]*langEntry // extension -> langEntry
}

func NewExtractor() *Extractor {
	e := &Extractor{langs: make(map[string]*langEntry)}

	// TypeScript
	tsP := sitter.NewParser()
	tsP.SetLanguage(typescript.GetLanguage())
	tsHandler := &TSHandler{}
	e.langs[".ts"] = &langEntry{parser: tsP, handler: tsHandler}

	// TSX
	tsxP := sitter.NewParser()
	tsxP.SetLanguage(tsx.GetLanguage())
	e.langs[".tsx"] = &langEntry{parser: tsxP, handler: tsHandler}

	// JavaScript
	jsP := sitter.NewParser()
	jsP.SetLanguage(javascript.GetLanguage())
	e.langs[".js"] = &langEntry{parser: jsP, handler: tsHandler}
	e.langs[".mjs"] = &langEntry{parser: jsP, handler: tsHandler}
	e.langs[".cjs"] = &langEntry{parser: jsP, handler: tsHandler}
	e.langs[".jsx"] = &langEntry{parser: jsP, handler: tsHandler}

	// Python
	pyP := sitter.NewParser()
	pyP.SetLanguage(python.GetLanguage())
	pyHandler := &PythonHandler{}
	e.langs[".py"] = &langEntry{parser: pyP, handler: pyHandler}

	// Go
	goP := sitter.NewParser()
	goP.SetLanguage(golang.GetLanguage())
	goHandler := &GoHandler{}
	e.langs[".go"] = &langEntry{parser: goP, handler: goHandler}

	// Swift
	swiftP := sitter.NewParser()
	swiftP.SetLanguage(swift.GetLanguage())
	swiftHandler := &SwiftHandler{}
	e.langs[".swift"] = &langEntry{parser: swiftP, handler: swiftHandler}

	return e
}

// FileHash computes the content hash of a file without parsing it.
func FileHash(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", md5.Sum(content)), nil
}

func (e *Extractor) ParseFile(filePath string) (*FileNode, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	entry, ok := e.langs[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported file type: %s", filePath)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	tree, err := entry.parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	symbols := entry.handler.ExtractSymbols(root, content, filePath)
	imports := entry.handler.ExtractImports(root, content)

	// Resolve references: walk each symbol's body to find which imports it actually uses
	importedNames := make(map[string]bool)
	for _, imp := range imports {
		for _, spec := range imp.Specifiers {
			importedNames[spec] = true
		}
	}
	if len(importedNames) > 0 {
		resolveReferences(root, content, symbols, importedNames)
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	hash := fmt.Sprintf("%x", md5.Sum(content))

	return &FileNode{
		Path:         filePath,
		Symbols:      symbols,
		Imports:      imports,
		LastModified: stat.ModTime().UnixMilli(),
		Hash:         hash,
	}, nil
}

// SupportedExtensions returns all file extensions the extractor can handle.
func (e *Extractor) SupportedExtensions() []string {
	exts := make([]string, 0, len(e.langs))
	for ext := range e.langs {
		exts = append(exts, ext)
	}
	return exts
}
