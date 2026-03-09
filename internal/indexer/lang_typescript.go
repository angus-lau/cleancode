package indexer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// TSHandler handles TypeScript, JavaScript, TSX, and JSX files.
type TSHandler struct{}

func (h *TSHandler) ExtractSymbols(root *sitter.Node, source []byte, filePath string) []Symbol {
	var symbols []Symbol
	tsWalkSymbols(root, source, filePath, &symbols, false)
	return symbols
}

func tsWalkSymbols(node *sitter.Node, source []byte, filePath string, symbols *[]Symbol, isExported bool) {
	nodeType := node.Type()

	switch nodeType {
	case "export_statement":
		for i := 0; i < int(node.ChildCount()); i++ {
			tsWalkSymbols(node.Child(i), source, filePath, symbols, true)
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

	if nodeType != "export_statement" {
		for i := 0; i < int(node.ChildCount()); i++ {
			tsWalkSymbols(node.Child(i), source, filePath, symbols, false)
		}
	}
}

func (h *TSHandler) ExtractImports(root *sitter.Node, source []byte) []ImportRef {
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
			Source:     sourcePath,
			Specifiers: specifiers,
			IsDefault:  isDefault,
			IsNamespace: isNamespace,
		})
	}

	return imports
}
