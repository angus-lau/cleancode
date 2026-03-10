package indexer

import (
	"context"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/swift"
)

func parseSwift(t *testing.T, source string) *sitter.Node {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(swift.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(source))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Cleanup(func() { tree.Close() })
	return tree.RootNode()
}

func TestSwiftExtractSymbols_Class(t *testing.T) {
	source := `class TradeService {
    static let shared = TradeService()
    var balance: Double = 0.0

    func buy(amount: Double) -> Bool {
        return true
    }
}`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	// Expect: TradeService (class), TradeService.shared (var), TradeService.balance (var),
	// TradeService.buy (method)
	assertSymbol(t, symbols, "TradeService", Class)
	assertSymbol(t, symbols, "TradeService.shared", Variable)
	assertSymbol(t, symbols, "TradeService.balance", Variable)
	assertSymbol(t, symbols, "TradeService.buy", Method)
}

func TestSwiftExtractSymbols_Struct(t *testing.T) {
	source := `struct TokenInfo {
    let name: String
    let supply: Int

    func formattedSupply() -> String {
        return "\(supply)"
    }
}`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	assertSymbol(t, symbols, "TokenInfo", Class) // structs map to Class
	assertSymbol(t, symbols, "TokenInfo.name", Variable)
	assertSymbol(t, symbols, "TokenInfo.supply", Variable)
	assertSymbol(t, symbols, "TokenInfo.formattedSupply", Method)
}

func TestSwiftExtractSymbols_Enum(t *testing.T) {
	source := `enum TradeStatus {
    case pending
    case completed
    case failed
}`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	assertSymbol(t, symbols, "TradeStatus", Enum)
	assertSymbol(t, symbols, "TradeStatus.pending", Variable)
	assertSymbol(t, symbols, "TradeStatus.completed", Variable)
	assertSymbol(t, symbols, "TradeStatus.failed", Variable)
}

func TestSwiftExtractSymbols_Protocol(t *testing.T) {
	source := `protocol Tradeable {
    func buy(amount: Double) -> Bool
    func sell(amount: Double) -> Bool
}`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	assertSymbol(t, symbols, "Tradeable", Interface)
	assertSymbol(t, symbols, "Tradeable.buy", Method)
	assertSymbol(t, symbols, "Tradeable.sell", Method)
}

func TestSwiftExtractSymbols_Extension(t *testing.T) {
	source := `extension TradeService {
    func getBalance() -> Double {
        return balance
    }
}`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	// Extension should not create a symbol for itself,
	// but should create TradeService.getBalance
	assertSymbol(t, symbols, "TradeService.getBalance", Method)
	assertNoSymbol(t, symbols, "TradeService", Class)
}

func TestSwiftExtractSymbols_TopLevelFunction(t *testing.T) {
	source := `func topLevelHelper(x: Int) -> Int {
    return x * 2
}`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	assertSymbol(t, symbols, "topLevelHelper", Function)
}

func TestSwiftExtractSymbols_TopLevelVariables(t *testing.T) {
	source := `let globalConstant = 42
var mutableGlobal = "hello"`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	assertSymbol(t, symbols, "globalConstant", Variable)
	assertSymbol(t, symbols, "mutableGlobal", Variable)
}

func TestSwiftExtractSymbols_Typealias(t *testing.T) {
	source := `typealias TokenID = String`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	assertSymbol(t, symbols, "TokenID", TypeAlias)
}

func TestSwiftExtractImports(t *testing.T) {
	source := `import Foundation
import UIKit
import SwiftUI`
	h := &SwiftHandler{}
	root := parseSwift(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(imports))
	}

	expectedModules := []string{"Foundation", "UIKit", "SwiftUI"}
	for i, imp := range imports {
		if imp.Source != expectedModules[i] {
			t.Errorf("import %d: expected source %q, got %q", i, expectedModules[i], imp.Source)
		}
		if len(imp.Specifiers) != 1 || imp.Specifiers[0] != expectedModules[i] {
			t.Errorf("import %d: expected specifier %q, got %v", i, expectedModules[i], imp.Specifiers)
		}
		if !imp.IsNamespace {
			t.Errorf("import %d: expected IsNamespace=true", i)
		}
	}
}

func TestSwiftExtractSymbols_FullFile(t *testing.T) {
	source := `import Foundation
import UIKit

protocol Tradeable {
    func buy(amount: Double) -> Bool
    func sell(amount: Double) -> Bool
}

enum TradeStatus {
    case pending
    case completed
    case failed
}

class TradeService: Tradeable {
    static let shared = TradeService()
    var balance: Double = 0.0

    func buy(amount: Double) -> Bool {
        balance += amount
        return true
    }

    func sell(amount: Double) -> Bool {
        balance -= amount
        return true
    }

    private func validate(_ trade: TradeStatus) -> Bool {
        switch trade {
        case .completed: return true
        default: return false
        }
    }
}

struct TokenInfo {
    let name: String
    let supply: Int

    func formattedSupply() -> String {
        return "\(supply)"
    }
}

extension TradeService {
    func getBalance() -> Double {
        return balance
    }
}

func topLevelHelper(x: Int) -> Int {
    return x * 2
}

let globalConstant = 42
var mutableGlobal = "hello"

typealias TokenID = String`

	h := &SwiftHandler{}
	root := parseSwift(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.swift")

	// Check we got a reasonable number of symbols
	if len(symbols) < 15 {
		t.Errorf("expected at least 15 symbols, got %d", len(symbols))
		for _, s := range symbols {
			t.Logf("  %s %s (%s:%d-%d)", s.Kind, s.Name, s.FilePath, s.StartLine, s.EndLine)
		}
	}

	// Verify key symbols exist
	assertSymbol(t, symbols, "Tradeable", Interface)
	assertSymbol(t, symbols, "TradeStatus", Enum)
	assertSymbol(t, symbols, "TradeService", Class)
	assertSymbol(t, symbols, "TradeService.buy", Method)
	assertSymbol(t, symbols, "TradeService.sell", Method)
	assertSymbol(t, symbols, "TradeService.validate", Method)
	assertSymbol(t, symbols, "TradeService.getBalance", Method) // from extension
	assertSymbol(t, symbols, "TokenInfo", Class)
	assertSymbol(t, symbols, "TokenInfo.formattedSupply", Method)
	assertSymbol(t, symbols, "topLevelHelper", Function)
	assertSymbol(t, symbols, "globalConstant", Variable)
	assertSymbol(t, symbols, "TokenID", TypeAlias)
}

// --- helpers ---

func assertSymbol(t *testing.T, symbols []Symbol, name string, kind SymbolKind) {
	t.Helper()
	for _, s := range symbols {
		if s.Name == name {
			if s.Kind != kind {
				t.Errorf("symbol %q: expected kind %q, got %q", name, kind, s.Kind)
			}
			return
		}
	}
	t.Errorf("symbol %q (%s) not found. Got:", name, kind)
	for _, s := range symbols {
		t.Logf("  %s %s", s.Kind, s.Name)
	}
}

func assertNoSymbol(t *testing.T, symbols []Symbol, name string, kind SymbolKind) {
	t.Helper()
	for _, s := range symbols {
		if s.Name == name && s.Kind == kind {
			t.Errorf("symbol %q (%s) should not exist but was found", name, kind)
			return
		}
	}
}
