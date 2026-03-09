package indexer

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type Extractor struct {
	tsParser  *sitter.Parser
	tsxParser *sitter.Parser
	jsParser  *sitter.Parser
}

func NewExtractor() *Extractor {
	tsP := sitter.NewParser()
	tsP.SetLanguage(typescript.GetLanguage())

	tsxP := sitter.NewParser()
	tsxP.SetLanguage(tsx.GetLanguage())

	jsP := sitter.NewParser()
	jsP.SetLanguage(javascript.GetLanguage())

	return &Extractor{
		tsParser:  tsP,
		tsxParser: tsxP,
		jsParser:  jsP,
	}
}

func (e *Extractor) ParseFile(filePath string) (*FileNode, error) {
	parser := e.parserForFile(filePath)
	if parser == nil {
		return nil, fmt.Errorf("unsupported file type: %s", filePath)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	symbols := extractSymbols(root, content, filePath)
	imports := extractImports(root, content)

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
		Hash:          hash,
	}, nil
}

func (e *Extractor) parserForFile(filePath string) *sitter.Parser {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".ts":
		return e.tsParser
	case ".tsx":
		return e.tsxParser
	case ".js", ".mjs", ".cjs", ".jsx":
		return e.jsParser
	default:
		return nil
	}
}

func extractSymbols(node *sitter.Node, source []byte, filePath string) []Symbol {
	var symbols []Symbol
	walkForSymbols(node, source, filePath, &symbols, false)
	return symbols
}

func walkForSymbols(node *sitter.Node, source []byte, filePath string, symbols *[]Symbol, isExported bool) {
	nodeType := node.Type()

	switch nodeType {
	case "export_statement":
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			walkForSymbols(child, source, filePath, symbols, true)
		}
		return

	case "function_declaration":
		if name := node.ChildByFieldName("name"); name != nil {
			sym := Symbol{
				Name:      name.Content(source),
				Kind:      Function,
				FilePath:  filePath,
				StartLine: int(node.StartPoint().Row) + 1,
				EndLine:   int(node.EndPoint().Row) + 1,
			}
			if isExported {
				sym.ExportName = sym.Name
			}
			*symbols = append(*symbols, sym)
		}

	case "class_declaration":
		if name := node.ChildByFieldName("name"); name != nil {
			className := name.Content(source)
			sym := Symbol{
				Name:      className,
				Kind:      Class,
				FilePath:  filePath,
				StartLine: int(node.StartPoint().Row) + 1,
				EndLine:   int(node.EndPoint().Row) + 1,
			}
			if isExported {
				sym.ExportName = sym.Name
			}
			*symbols = append(*symbols, sym)

			// Extract methods
			if body := node.ChildByFieldName("body"); body != nil {
				for i := 0; i < int(body.ChildCount()); i++ {
					member := body.Child(i)
					if member.Type() == "method_definition" {
						if methodName := member.ChildByFieldName("name"); methodName != nil {
							*symbols = append(*symbols, Symbol{
								Name:      className + "." + methodName.Content(source),
								Kind:      Method,
								FilePath:  filePath,
								StartLine: int(member.StartPoint().Row) + 1,
								EndLine:   int(member.EndPoint().Row) + 1,
							})
						}
					}
				}
			}
		}

	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(node.ChildCount()); i++ {
			declarator := node.Child(i)
			if declarator.Type() == "variable_declarator" {
				if name := declarator.ChildByFieldName("name"); name != nil {
					kind := Variable
					if value := declarator.ChildByFieldName("value"); value != nil {
						if value.Type() == "arrow_function" || value.Type() == "function" {
							kind = Function
						}
					}
					sym := Symbol{
						Name:      name.Content(source),
						Kind:      kind,
						FilePath:  filePath,
						StartLine: int(node.StartPoint().Row) + 1,
						EndLine:   int(node.EndPoint().Row) + 1,
					}
					if isExported {
						sym.ExportName = sym.Name
					}
					*symbols = append(*symbols, sym)
				}
			}
		}

	case "interface_declaration":
		if name := node.ChildByFieldName("name"); name != nil {
			sym := Symbol{
				Name:      name.Content(source),
				Kind:      Interface,
				FilePath:  filePath,
				StartLine: int(node.StartPoint().Row) + 1,
				EndLine:   int(node.EndPoint().Row) + 1,
			}
			if isExported {
				sym.ExportName = sym.Name
			}
			*symbols = append(*symbols, sym)
		}

	case "type_alias_declaration":
		if name := node.ChildByFieldName("name"); name != nil {
			sym := Symbol{
				Name:      name.Content(source),
				Kind:      TypeAlias,
				FilePath:  filePath,
				StartLine: int(node.StartPoint().Row) + 1,
				EndLine:   int(node.EndPoint().Row) + 1,
			}
			if isExported {
				sym.ExportName = sym.Name
			}
			*symbols = append(*symbols, sym)
		}

	case "enum_declaration":
		if name := node.ChildByFieldName("name"); name != nil {
			sym := Symbol{
				Name:      name.Content(source),
				Kind:      Enum,
				FilePath:  filePath,
				StartLine: int(node.StartPoint().Row) + 1,
				EndLine:   int(node.EndPoint().Row) + 1,
			}
			if isExported {
				sym.ExportName = sym.Name
			}
			*symbols = append(*symbols, sym)
		}
	}

	// Recurse into children (unless we already handled them)
	if nodeType != "export_statement" {
		for i := 0; i < int(node.ChildCount()); i++ {
			walkForSymbols(node.Child(i), source, filePath, symbols, false)
		}
	}
}

func extractImports(root *sitter.Node, source []byte) []ImportRef {
	var imports []ImportRef

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_statement" {
			continue
		}

		srcNode := child.ChildByFieldName("source")
		if srcNode == nil {
			continue
		}

		sourcePath := strings.Trim(srcNode.Content(source), "'\"")
		var specifiers []string
		isDefault := false
		isNamespace := false

		for j := 0; j < int(child.ChildCount()); j++ {
			part := child.Child(j)
			if part.Type() == "import_clause" {
				for k := 0; k < int(part.ChildCount()); k++ {
					spec := part.Child(k)
					switch spec.Type() {
					case "identifier":
						specifiers = append(specifiers, spec.Content(source))
						isDefault = true
					case "named_imports":
						for l := 0; l < int(spec.ChildCount()); l++ {
							named := spec.Child(l)
							if named.Type() == "import_specifier" {
								if alias := named.ChildByFieldName("alias"); alias != nil {
									specifiers = append(specifiers, alias.Content(source))
								} else if name := named.ChildByFieldName("name"); name != nil {
									specifiers = append(specifiers, name.Content(source))
								}
							}
						}
					case "namespace_import":
						if name := spec.ChildByFieldName("name"); name != nil {
							specifiers = append(specifiers, name.Content(source))
						}
						isNamespace = true
					}
				}
			}
		}

		imports = append(imports, ImportRef{
			Source:      sourcePath,
			Specifiers:  specifiers,
			IsDefault:   isDefault,
			IsNamespace: isNamespace,
		})
	}

	return imports
}
