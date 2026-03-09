package indexer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// PythonHandler handles .py files.
type PythonHandler struct{}

func (h *PythonHandler) ExtractSymbols(root *sitter.Node, source []byte, filePath string) []Symbol {
	var symbols []Symbol
	pyWalkSymbols(root, source, filePath, &symbols, "")
	return symbols
}

func pyWalkSymbols(node *sitter.Node, source []byte, filePath string, symbols *[]Symbol, className string) {
	nodeType := node.Type()

	switch nodeType {
	case "function_definition":
		if name := node.ChildByFieldName("name"); name != nil {
			funcName := name.Content(source)
			kind := Function

			if className != "" {
				funcName = className + "." + funcName
				kind = Method
			}

			*symbols = append(*symbols, Symbol{
				Name:       funcName,
				Kind:       kind,
				FilePath:   filePath,
				StartLine:  int(node.StartPoint().Row) + 1,
				EndLine:    int(node.EndPoint().Row) + 1,
				ExportName: funcName, // Python: everything at module level is "exported"
			})
		}
		// Don't recurse into nested functions by default
		return

	case "class_definition":
		if name := node.ChildByFieldName("name"); name != nil {
			clsName := name.Content(source)
			*symbols = append(*symbols, Symbol{
				Name:       clsName,
				Kind:       Class,
				FilePath:   filePath,
				StartLine:  int(node.StartPoint().Row) + 1,
				EndLine:    int(node.EndPoint().Row) + 1,
				ExportName: clsName,
			})

			// Walk class body for methods
			if body := node.ChildByFieldName("body"); body != nil {
				for i := 0; i < int(body.ChildCount()); i++ {
					pyWalkSymbols(body.Child(i), source, filePath, symbols, clsName)
				}
			}
		}
		return

	case "decorated_definition":
		// Walk into the decorated function/class
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "function_definition" || child.Type() == "class_definition" {
				pyWalkSymbols(child, source, filePath, symbols, className)
			}
		}
		return

	case "assignment":
		// Top-level assignments: e.g. `MY_CONST = 42`
		if className == "" {
			if left := node.ChildByFieldName("left"); left != nil {
				if left.Type() == "identifier" {
					varName := left.Content(source)
					// Skip private/dunder names
					if !strings.HasPrefix(varName, "_") {
						*symbols = append(*symbols, Symbol{
							Name:       varName,
							Kind:       Variable,
							FilePath:   filePath,
							StartLine:  int(node.StartPoint().Row) + 1,
							EndLine:    int(node.EndPoint().Row) + 1,
							ExportName: varName,
						})
					}
				}
			}
		}
	}

	// Recurse
	for i := 0; i < int(node.ChildCount()); i++ {
		pyWalkSymbols(node.Child(i), source, filePath, symbols, className)
	}
}

func (h *PythonHandler) ExtractImports(root *sitter.Node, source []byte) []ImportRef {
	var imports []ImportRef

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)

		switch child.Type() {
		case "import_statement":
			// import foo, import foo.bar
			for j := 0; j < int(child.ChildCount()); j++ {
				name := child.Child(j)
				if name.Type() == "dotted_name" || name.Type() == "aliased_import" {
					modName := name.Content(source)
					// For aliased: "import foo as f" -> use alias
					if name.Type() == "aliased_import" {
						if alias := name.ChildByFieldName("alias"); alias != nil {
							modName = alias.Content(source)
						} else if nameNode := name.ChildByFieldName("name"); nameNode != nil {
							modName = nameNode.Content(source)
						}
					}
					imports = append(imports, ImportRef{
						Source:      name.Content(source),
						Specifiers:  []string{modName},
						IsDefault:   true,
						IsNamespace: false,
					})
				}
			}

		case "import_from_statement":
			// from foo import bar, baz
			var moduleName string
			if modNode := child.ChildByFieldName("module_name"); modNode != nil {
				moduleName = modNode.Content(source)
			}

			var specifiers []string
			isNamespace := false

			for j := 0; j < int(child.ChildCount()); j++ {
				part := child.Child(j)
				switch part.Type() {
				case "dotted_name":
					// This could be the module name or an imported name
					if moduleName == "" {
						moduleName = part.Content(source)
					}
				case "import_prefix":
					// relative import dots
					if moduleName == "" {
						moduleName = part.Content(source)
					}
				case "aliased_import":
					if alias := part.ChildByFieldName("alias"); alias != nil {
						specifiers = append(specifiers, alias.Content(source))
					} else if nameNode := part.ChildByFieldName("name"); nameNode != nil {
						specifiers = append(specifiers, nameNode.Content(source))
					}
				case "wildcard_import":
					isNamespace = true
					specifiers = append(specifiers, "*")
				}
			}

			// If we still don't have specifiers, scan for plain identifiers after "import"
			if len(specifiers) == 0 {
				seenImport := false
				for j := 0; j < int(child.ChildCount()); j++ {
					part := child.Child(j)
					if part.Content(source) == "import" {
						seenImport = true
						continue
					}
					if seenImport && part.Type() == "dotted_name" {
						specifiers = append(specifiers, part.Content(source))
					}
				}
			}

			if moduleName != "" {
				imports = append(imports, ImportRef{
					Source:      moduleName,
					Specifiers:  specifiers,
					IsDefault:   false,
					IsNamespace: isNamespace,
				})
			}
		}
	}

	return imports
}
