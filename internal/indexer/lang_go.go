package indexer

import (
	"strings"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
)

// GoHandler handles .go files.
type GoHandler struct{}

func (h *GoHandler) ExtractSymbols(root *sitter.Node, source []byte, filePath string) []Symbol {
	var symbols []Symbol
	goWalkSymbols(root, source, filePath, &symbols)
	return symbols
}

func goWalkSymbols(node *sitter.Node, source []byte, filePath string, symbols *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "function_declaration":
			if name := child.ChildByFieldName("name"); name != nil {
				funcName := name.Content(source)
				sym := Symbol{
					Name:      funcName,
					Kind:      Function,
					FilePath:  filePath,
					StartLine: int(child.StartPoint().Row) + 1,
					EndLine:   int(child.EndPoint().Row) + 1,
				}
				if isGoExported(funcName) {
					sym.ExportName = funcName
				}
				*symbols = append(*symbols, sym)
			}

		case "method_declaration":
			if name := child.ChildByFieldName("name"); name != nil {
				methodName := name.Content(source)
				// Get receiver type
				receiverName := ""
				if receiver := child.ChildByFieldName("receiver"); receiver != nil {
					// Walk to find the type identifier
					goFindReceiverType(receiver, source, &receiverName)
				}

				fullName := methodName
				if receiverName != "" {
					fullName = receiverName + "." + methodName
				}

				sym := Symbol{
					Name:      fullName,
					Kind:      Method,
					FilePath:  filePath,
					StartLine: int(child.StartPoint().Row) + 1,
					EndLine:   int(child.EndPoint().Row) + 1,
				}
				if isGoExported(methodName) {
					sym.ExportName = fullName
				}
				*symbols = append(*symbols, sym)
			}

		case "type_declaration":
			// type Foo struct { ... } or type Foo interface { ... }
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "type_spec" {
					if name := spec.ChildByFieldName("name"); name != nil {
						typeName := name.Content(source)
						kind := TypeAlias

						// Determine if it's a struct, interface, or type alias
						if typeNode := spec.ChildByFieldName("type"); typeNode != nil {
							switch typeNode.Type() {
							case "struct_type":
								kind = Class // treat structs as classes
							case "interface_type":
								kind = Interface
							}
						}

						sym := Symbol{
							Name:      typeName,
							Kind:      kind,
							FilePath:  filePath,
							StartLine: int(spec.StartPoint().Row) + 1,
							EndLine:   int(spec.EndPoint().Row) + 1,
						}
						if isGoExported(typeName) {
							sym.ExportName = typeName
						}
						*symbols = append(*symbols, sym)
					}
				}
			}

		case "var_declaration", "const_declaration":
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "var_spec" || spec.Type() == "const_spec" {
					// Can have multiple names: var a, b int
					if name := spec.ChildByFieldName("name"); name != nil {
						varName := name.Content(source)
						sym := Symbol{
							Name:      varName,
							Kind:      Variable,
							FilePath:  filePath,
							StartLine: int(spec.StartPoint().Row) + 1,
							EndLine:   int(spec.EndPoint().Row) + 1,
						}
						if isGoExported(varName) {
							sym.ExportName = varName
						}
						*symbols = append(*symbols, sym)
					}
				}
			}
		}
	}
}

func goFindReceiverType(node *sitter.Node, source []byte, name *string) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "parameter_declaration":
			if typeNode := child.ChildByFieldName("type"); typeNode != nil {
				goExtractTypeName(typeNode, source, name)
			}
		}
		if *name == "" {
			goFindReceiverType(child, source, name)
		}
	}
}

func goExtractTypeName(node *sitter.Node, source []byte, name *string) {
	switch node.Type() {
	case "type_identifier":
		*name = node.Content(source)
	case "pointer_type":
		// *Foo -> Foo
		for i := 0; i < int(node.ChildCount()); i++ {
			goExtractTypeName(node.Child(i), source, name)
		}
	}
}

// isGoExported checks if a Go identifier is exported (starts with uppercase).
func isGoExported(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

func (h *GoHandler) ExtractImports(root *sitter.Node, source []byte) []ImportRef {
	var imports []ImportRef

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_declaration" {
			continue
		}

		for j := 0; j < int(child.ChildCount()); j++ {
			spec := child.Child(j)

			switch spec.Type() {
			case "import_spec":
				imp := goParseImportSpec(spec, source)
				if imp != nil {
					imports = append(imports, *imp)
				}
			case "import_spec_list":
				for k := 0; k < int(spec.ChildCount()); k++ {
					inner := spec.Child(k)
					if inner.Type() == "import_spec" {
						imp := goParseImportSpec(inner, source)
						if imp != nil {
							imports = append(imports, *imp)
						}
					}
				}
			}
		}
	}

	return imports
}

func goParseImportSpec(spec *sitter.Node, source []byte) *ImportRef {
	if path := spec.ChildByFieldName("path"); path != nil {
		importPath := strings.Trim(path.Content(source), "\"")

		// Get the package name (last segment of path, or alias)
		pkgName := importPath
		if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
			pkgName = importPath[idx+1:]
		}

		isNamespace := false
		if name := spec.ChildByFieldName("name"); name != nil {
			alias := name.Content(source)
			if alias == "." {
				isNamespace = true // dot import
			} else if alias != "_" {
				pkgName = alias
			}
		}

		return &ImportRef{
			Source:      importPath,
			Specifiers:  []string{pkgName},
			IsDefault:   false,
			IsNamespace: isNamespace,
		}
	}
	return nil
}
