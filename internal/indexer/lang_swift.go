package indexer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// SwiftHandler handles .swift files.
type SwiftHandler struct{}

func (h *SwiftHandler) ExtractSymbols(root *sitter.Node, source []byte, filePath string) []Symbol {
	var symbols []Symbol
	swiftWalkSymbols(root, source, filePath, &symbols, "")
	return symbols
}

func swiftWalkSymbols(node *sitter.Node, source []byte, filePath string, symbols *[]Symbol, parentType string) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "class_declaration":
			swiftHandleClassDeclaration(child, source, filePath, symbols)

		case "protocol_declaration":
			if name := child.ChildByFieldName("name"); name != nil {
				protoName := name.Content(source)
				*symbols = append(*symbols, Symbol{
					Name:       protoName,
					Kind:       Interface, // map protocol -> interface
					FilePath:   filePath,
					StartLine:  int(child.StartPoint().Row) + 1,
					EndLine:    int(child.EndPoint().Row) + 1,
					ExportName: protoName, // Swift protocols are always public-ish
				})

				// Extract protocol method declarations
				if body := child.ChildByFieldName("body"); body != nil {
					for j := 0; j < int(body.ChildCount()); j++ {
						member := body.Child(j)
						if member.Type() == "protocol_function_declaration" {
							if methodName := swiftFuncName(member, source); methodName != "" {
								*symbols = append(*symbols, Symbol{
									Name:      protoName + "." + methodName,
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

		case "function_declaration":
			if funcName := swiftFuncName(child, source); funcName != "" {
				kind := Function
				if parentType != "" {
					kind = Method
					funcName = parentType + "." + funcName
				}
				*symbols = append(*symbols, Symbol{
					Name:      funcName,
					Kind:      kind,
					FilePath:  filePath,
					StartLine: int(child.StartPoint().Row) + 1,
					EndLine:   int(child.EndPoint().Row) + 1,
				})
			}

		case "property_declaration":
			if propName := swiftPropertyName(child, source); propName != "" {
				sym := Symbol{
					Name:      propName,
					Kind:      Variable,
					FilePath:  filePath,
					StartLine: int(child.StartPoint().Row) + 1,
					EndLine:   int(child.EndPoint().Row) + 1,
				}
				if parentType != "" {
					sym.Name = parentType + "." + propName
				}
				*symbols = append(*symbols, sym)
			}

		case "typealias_declaration":
			if name := child.ChildByFieldName("name"); name != nil {
				aliasName := name.Content(source)
				*symbols = append(*symbols, Symbol{
					Name:       aliasName,
					Kind:       TypeAlias,
					FilePath:   filePath,
					StartLine:  int(child.StartPoint().Row) + 1,
					EndLine:    int(child.EndPoint().Row) + 1,
					ExportName: aliasName,
				})
			}
		}
	}
}

func swiftHandleClassDeclaration(node *sitter.Node, source []byte, filePath string, symbols *[]Symbol) {
	// Determine the declaration kind: class, struct, enum, or extension
	declKind := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		fieldName := node.FieldNameForChild(i)
		if fieldName == "declaration_kind" {
			declKind = child.Type()
			break
		}
	}

	// Get the type name
	typeName := ""
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		if nameNode.Type() == "type_identifier" {
			typeName = nameNode.Content(source)
		} else if nameNode.Type() == "user_type" {
			// Extensions use user_type -> type_identifier
			for j := 0; j < int(nameNode.ChildCount()); j++ {
				child := nameNode.Child(j)
				if child.Type() == "type_identifier" {
					typeName = child.Content(source)
					break
				}
			}
		}
	}

	if typeName == "" {
		return
	}

	// Map declaration kind to SymbolKind
	var kind SymbolKind
	switch declKind {
	case "class":
		kind = Class
	case "struct":
		kind = Class // treat structs as classes, like Go handler
	case "enum":
		kind = Enum
	case "extension":
		// Don't create a symbol for the extension itself,
		// but extract its members prefixed with the type name
		if body := node.ChildByFieldName("body"); body != nil {
			swiftWalkSymbols(body, source, filePath, symbols, typeName)
		}
		return
	default:
		kind = Class
	}

	sym := Symbol{
		Name:       typeName,
		Kind:       kind,
		FilePath:   filePath,
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		ExportName: typeName,
	}
	*symbols = append(*symbols, sym)

	// Extract members from class/struct/enum body
	if body := node.ChildByFieldName("body"); body != nil {
		swiftWalkSymbols(body, source, filePath, symbols, typeName)

		// For enums, also extract enum_entry cases
		if kind == Enum {
			swiftExtractEnumCases(body, source, filePath, symbols, typeName)
		}
	}
}

func swiftExtractEnumCases(body *sitter.Node, source []byte, filePath string, symbols *[]Symbol, enumName string) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() == "enum_entry" {
			if name := child.ChildByFieldName("name"); name != nil {
				caseName := name.Content(source)
				*symbols = append(*symbols, Symbol{
					Name:      enumName + "." + caseName,
					Kind:      Variable, // enum cases as variables
					FilePath:  filePath,
					StartLine: int(child.StartPoint().Row) + 1,
					EndLine:   int(child.EndPoint().Row) + 1,
				})
			}
		}
	}
}

// swiftFuncName extracts the function name from a function_declaration node.
func swiftFuncName(node *sitter.Node, source []byte) string {
	// The @name field in Swift function declarations is a simple_identifier
	if name := node.ChildByFieldName("name"); name != nil {
		if name.Type() == "simple_identifier" {
			return name.Content(source)
		}
	}
	// Fallback: find first simple_identifier after "func" keyword
	foundFunc := false
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "func" {
			foundFunc = true
			continue
		}
		if foundFunc && child.Type() == "simple_identifier" {
			return child.Content(source)
		}
	}
	return ""
}

// swiftPropertyName extracts the variable/property name from a property_declaration.
// Pattern: property_declaration > @name > pattern > @bound_identifier > simple_identifier
func swiftPropertyName(node *sitter.Node, source []byte) string {
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		if nameNode.Type() == "pattern" {
			if bound := nameNode.ChildByFieldName("bound_identifier"); bound != nil {
				return bound.Content(source)
			}
			// Fallback: find simple_identifier in pattern
			for i := 0; i < int(nameNode.ChildCount()); i++ {
				child := nameNode.Child(i)
				if child.Type() == "simple_identifier" {
					return child.Content(source)
				}
			}
		} else if nameNode.Type() == "simple_identifier" {
			return nameNode.Content(source)
		}
	}
	return ""
}

func (h *SwiftHandler) ExtractImports(root *sitter.Node, source []byte) []ImportRef {
	var imports []ImportRef

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_declaration" {
			continue
		}

		// import_declaration > identifier > simple_identifier
		for j := 0; j < int(child.ChildCount()); j++ {
			part := child.Child(j)
			if part.Type() == "identifier" {
				moduleName := ""
				// identifier may contain simple_identifier children
				for k := 0; k < int(part.ChildCount()); k++ {
					sub := part.Child(k)
					if sub.Type() == "simple_identifier" {
						if moduleName != "" {
							moduleName += "."
						}
						moduleName += sub.Content(source)
					}
				}
				if moduleName == "" {
					moduleName = part.Content(source)
				}

				// In Swift, importing a module makes all its public symbols available.
				// We treat the module name as the specifier for reference resolution.
				imports = append(imports, ImportRef{
					Source:      moduleName,
					Specifiers:  []string{moduleName},
					IsDefault:   false,
					IsNamespace: true, // Swift imports are namespace-level
				})
			}
		}
	}

	return imports
}

// swiftAccessLevel checks if a declaration has a visibility modifier.
func swiftIsPublic(node *sitter.Node, source []byte) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "modifiers" {
			content := strings.ToLower(child.Content(source))
			if strings.Contains(content, "public") || strings.Contains(content, "open") {
				return true
			}
			if strings.Contains(content, "private") || strings.Contains(content, "fileprivate") {
				return false
			}
		}
	}
	// Default: internal (visible within module)
	return true
}
