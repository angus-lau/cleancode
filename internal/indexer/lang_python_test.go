package indexer

import (
	"context"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

func parsePython(t *testing.T, source string) *sitter.Node {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(python.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(source))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Cleanup(func() { tree.Close() })
	return tree.RootNode()
}

// --- Symbol extraction ---

func TestPythonExtractSymbols_Function(t *testing.T) {
	source := `def greet(name: str) -> str:
    return f"hello {name}"

def add(a: int, b: int) -> int:
    return a + b`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "greet", Function)
	assertSymbol(t, symbols, "add", Function)

	if len(symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(symbols))
	}

	// Python module-level functions are always "exported"
	for _, s := range symbols {
		if s.ExportName == "" {
			t.Errorf("symbol %q should have ExportName set (Python module-level)", s.Name)
		}
	}
}

func TestPythonExtractSymbols_Class(t *testing.T) {
	source := `class UserService:
    def __init__(self, db):
        self.db = db

    def get_user(self, user_id: str):
        return self.db.find(user_id)

    def delete_user(self, user_id: str):
        self.db.delete(user_id)`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "UserService", Class)
	assertSymbol(t, symbols, "UserService.__init__", Method)
	assertSymbol(t, symbols, "UserService.get_user", Method)
	assertSymbol(t, symbols, "UserService.delete_user", Method)
}

func TestPythonExtractSymbols_ClassInheritance(t *testing.T) {
	source := `class Animal:
    def speak(self):
        pass

class Dog(Animal):
    def speak(self):
        return "woof"

    def fetch(self, item):
        return item`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "Animal", Class)
	assertSymbol(t, symbols, "Animal.speak", Method)
	assertSymbol(t, symbols, "Dog", Class)
	assertSymbol(t, symbols, "Dog.speak", Method)
	assertSymbol(t, symbols, "Dog.fetch", Method)
}

func TestPythonExtractSymbols_DecoratedFunction(t *testing.T) {
	source := `@app.route("/users")
def list_users():
    return []

@staticmethod
def helper():
    pass`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "list_users", Function)
	assertSymbol(t, symbols, "helper", Function)
}

func TestPythonExtractSymbols_DecoratedClass(t *testing.T) {
	source := `@dataclass
class Config:
    host: str
    port: int

    def url(self) -> str:
        return f"{self.host}:{self.port}"`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "Config", Class)
	assertSymbol(t, symbols, "Config.url", Method)
}

func TestPythonExtractSymbols_TopLevelAssignment(t *testing.T) {
	source := `MAX_RETRIES = 3
DEFAULT_TIMEOUT = 30
API_VERSION = "v2"
BASE_URL = "https://api.example.com"`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "MAX_RETRIES", Variable)
	assertSymbol(t, symbols, "DEFAULT_TIMEOUT", Variable)
	assertSymbol(t, symbols, "API_VERSION", Variable)
	assertSymbol(t, symbols, "BASE_URL", Variable)

	if len(symbols) != 4 {
		t.Errorf("expected 4 symbols, got %d", len(symbols))
	}
}

func TestPythonExtractSymbols_PrivateSkipped(t *testing.T) {
	source := `PUBLIC_VAR = 42
_private_var = "secret"
__dunder_var = "hidden"
_another_private = 0`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "PUBLIC_VAR", Variable)
	assertNoSymbol(t, symbols, "_private_var", Variable)
	assertNoSymbol(t, symbols, "__dunder_var", Variable)
	assertNoSymbol(t, symbols, "_another_private", Variable)

	if len(symbols) != 1 {
		t.Errorf("expected 1 symbol (only public), got %d", len(symbols))
		for _, s := range symbols {
			t.Logf("  %s %s", s.Kind, s.Name)
		}
	}
}

func TestPythonExtractSymbols_NestedFunctionsIgnored(t *testing.T) {
	source := `def outer():
    def inner():
        pass
    return inner()`
	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	assertSymbol(t, symbols, "outer", Function)
	// inner should not be extracted (nested function)
	assertNoSymbol(t, symbols, "inner", Function)
}

// --- Import extraction ---

func TestPythonExtractImports_Import(t *testing.T) {
	source := `import os
import sys
import json`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(imports))
	}

	expected := []string{"os", "sys", "json"}
	for i, imp := range imports {
		if imp.Source != expected[i] {
			t.Errorf("import %d: expected source %q, got %q", i, expected[i], imp.Source)
		}
		if !imp.IsDefault {
			t.Errorf("import %d: plain import should be IsDefault=true", i)
		}
	}
}

func TestPythonExtractImports_ImportDotted(t *testing.T) {
	source := `import os.path
import urllib.parse`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	if imports[0].Source != "os.path" {
		t.Errorf("import 0: expected source %q, got %q", "os.path", imports[0].Source)
	}
}

func TestPythonExtractImports_FromImport(t *testing.T) {
	source := `from typing import List, Dict, Optional
from pathlib import Path`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	if imports[0].Source != "typing" {
		t.Errorf("import 0: expected source %q, got %q", "typing", imports[0].Source)
	}
	if len(imports[0].Specifiers) != 3 {
		t.Errorf("import 0: expected 3 specifiers, got %d: %v", len(imports[0].Specifiers), imports[0].Specifiers)
	}
	if imports[0].IsDefault {
		t.Error("import 0: from-import should not be IsDefault")
	}
}

func TestPythonExtractImports_Aliased(t *testing.T) {
	source := `import numpy as np
import pandas as pd`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	// Aliased imports should use the alias as specifier
	found := false
	for _, imp := range imports {
		for _, s := range imp.Specifiers {
			if s == "np" || s == "pd" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected aliases 'np'/'pd' in specifiers, got %+v", imports)
	}
}

func TestPythonExtractImports_FromAliased(t *testing.T) {
	source := `from datetime import datetime as dt, timedelta as td`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}

	imp := imports[0]
	if imp.Source != "datetime" {
		t.Errorf("expected source %q, got %q", "datetime", imp.Source)
	}

	found := map[string]bool{}
	for _, s := range imp.Specifiers {
		found[s] = true
	}
	if !found["dt"] {
		t.Errorf("expected alias 'dt' in specifiers, got %v", imp.Specifiers)
	}
	if !found["td"] {
		t.Errorf("expected alias 'td' in specifiers, got %v", imp.Specifiers)
	}
}

func TestPythonExtractImports_Wildcard(t *testing.T) {
	source := `from os.path import *`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}

	if !imports[0].IsNamespace {
		t.Error("wildcard import should be IsNamespace=true")
	}
	if imports[0].Source != "os.path" {
		t.Errorf("expected source %q, got %q", "os.path", imports[0].Source)
	}
}

func TestPythonExtractImports_Relative(t *testing.T) {
	source := `from . import utils
from ..models import User`
	h := &PythonHandler{}
	root := parsePython(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	// Both should be from-imports (not default)
	for i, imp := range imports {
		if imp.IsDefault {
			t.Errorf("import %d: relative from-import should not be IsDefault", i)
		}
	}
}

// --- Full file ---

func TestPythonExtractSymbols_FullFile(t *testing.T) {
	source := `import os
from typing import List, Optional
from dataclasses import dataclass

MAX_CONNECTIONS = 100
DEFAULT_TIMEOUT = 30

@dataclass
class DatabaseConfig:
    host: str
    port: int
    database: str

    def connection_string(self) -> str:
        return f"{self.host}:{self.port}/{self.database}"

class UserRepository:
    def __init__(self, config: DatabaseConfig):
        self.config = config

    def find_by_id(self, user_id: str) -> Optional[dict]:
        pass

    def find_all(self) -> List[dict]:
        pass

    def save(self, user: dict) -> None:
        pass

@app.route("/health")
def health_check():
    return {"status": "ok"}

def create_repository(config: DatabaseConfig) -> UserRepository:
    return UserRepository(config)`

	h := &PythonHandler{}
	root := parsePython(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.py")

	if len(symbols) < 10 {
		t.Errorf("expected at least 10 symbols, got %d", len(symbols))
		for _, s := range symbols {
			t.Logf("  %s %s (%s:%d-%d)", s.Kind, s.Name, s.FilePath, s.StartLine, s.EndLine)
		}
	}

	// Variables
	assertSymbol(t, symbols, "MAX_CONNECTIONS", Variable)
	assertSymbol(t, symbols, "DEFAULT_TIMEOUT", Variable)

	// Classes and methods
	assertSymbol(t, symbols, "DatabaseConfig", Class)
	assertSymbol(t, symbols, "DatabaseConfig.connection_string", Method)
	assertSymbol(t, symbols, "UserRepository", Class)
	assertSymbol(t, symbols, "UserRepository.__init__", Method)
	assertSymbol(t, symbols, "UserRepository.find_by_id", Method)
	assertSymbol(t, symbols, "UserRepository.find_all", Method)
	assertSymbol(t, symbols, "UserRepository.save", Method)

	// Functions (including decorated)
	assertSymbol(t, symbols, "health_check", Function)
	assertSymbol(t, symbols, "create_repository", Function)

	// Verify imports
	imports := h.ExtractImports(root, []byte(source))
	if len(imports) != 3 {
		t.Errorf("expected 3 imports, got %d", len(imports))
	}
}
