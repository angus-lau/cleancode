package indexer

import (
	sitter "github.com/smacker/go-tree-sitter"
)

// resolveReferences walks each symbol's AST subtree to find which imported
// names are actually referenced inside the symbol's body. This gives us
// precise call-site edges instead of "every symbol uses every import."
func resolveReferences(root *sitter.Node, source []byte, symbols []Symbol, importedNames map[string]bool) {
	// Build a map of symbols by their start/end lines for quick lookup
	for i := range symbols {
		sym := &symbols[i]

		// Only resolve for functions, methods, and variables (arrow functions)
		// Classes/interfaces/types don't "call" things in the same way
		if sym.Kind != Function && sym.Kind != Method && sym.Kind != Variable {
			continue
		}

		// Find the AST node that corresponds to this symbol's range
		node := findNodeAtRange(root, sym.StartLine-1, sym.EndLine-1) // tree-sitter is 0-indexed
		if node == nil {
			continue
		}

		// Walk the node's subtree to find all identifier references
		refs := make(map[string]bool)
		collectIdentifiers(node, source, importedNames, refs)

		if len(refs) > 0 {
			sym.References = make([]string, 0, len(refs))
			for name := range refs {
				sym.References = append(sym.References, name)
			}
		}
	}
}

// findNodeAtRange finds the deepest node that spans the given line range.
// startRow and endRow are 0-indexed (tree-sitter convention).
func findNodeAtRange(node *sitter.Node, startRow, endRow int) *sitter.Node {
	nodeStart := int(node.StartPoint().Row)
	nodeEnd := int(node.EndPoint().Row)

	// Check if this node matches our target range
	if nodeStart == startRow && nodeEnd == endRow {
		return node
	}

	// If this node doesn't contain our range, skip it
	if nodeStart > startRow || nodeEnd < endRow {
		return nil
	}

	// Try children for a more precise match
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if found := findNodeAtRange(child, startRow, endRow); found != nil {
			return found
		}
	}

	// If no child matched exactly but this node contains the range, use it
	if nodeStart <= startRow && nodeEnd >= endRow {
		return node
	}

	return nil
}

// collectIdentifiers walks an AST subtree and collects all identifier names
// that match an imported name. It skips declaration sites (the name being
// defined) and focuses on usage sites.
func collectIdentifiers(node *sitter.Node, source []byte, importedNames map[string]bool, refs map[string]bool) {
	nodeType := node.Type()

	// Skip the declaration name itself — we want references, not definitions.
	// These are the field names used by tree-sitter for the "name" being declared.
	if nodeType == "function_declaration" || nodeType == "function_definition" ||
		nodeType == "method_declaration" || nodeType == "method_definition" ||
		nodeType == "class_declaration" || nodeType == "class_definition" ||
		nodeType == "variable_declarator" || nodeType == "type_alias_declaration" ||
		nodeType == "interface_declaration" {
		// Walk children but skip the "name" field
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			// Skip the name field of declarations
			if node.FieldNameForChild(i) == "name" {
				continue
			}
			collectIdentifiers(child, source, importedNames, refs)
		}
		return
	}

	// For identifiers, check if they match an imported name
	if nodeType == "identifier" || nodeType == "type_identifier" {
		name := node.Content(source)
		if importedNames[name] {
			refs[name] = true
		}
		return
	}

	// For member expressions like `foo.bar()`, only check the object part
	if nodeType == "member_expression" || nodeType == "member_access_expression" {
		if obj := node.ChildByFieldName("object"); obj != nil {
			collectIdentifiers(obj, source, importedNames, refs)
		}
		return
	}

	// For call expressions, check the function being called
	if nodeType == "call_expression" {
		if fn := node.ChildByFieldName("function"); fn != nil {
			collectIdentifiers(fn, source, importedNames, refs)
		}
		// Also check arguments
		if args := node.ChildByFieldName("arguments"); args != nil {
			collectIdentifiers(args, source, importedNames, refs)
		}
		return
	}

	// Recurse into all children
	for i := 0; i < int(node.ChildCount()); i++ {
		collectIdentifiers(node.Child(i), source, importedNames, refs)
	}
}
