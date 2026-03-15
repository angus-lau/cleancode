package indexer

import (
	"context"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

func parseTS(t *testing.T, source string) *sitter.Node {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(typescript.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(source))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Cleanup(func() { tree.Close() })
	return tree.RootNode()
}

func parseJS(t *testing.T, source string) *sitter.Node {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(javascript.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(source))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	t.Cleanup(func() { tree.Close() })
	return tree.RootNode()
}

// --- Symbol extraction ---

func TestTSExtractSymbols_Function(t *testing.T) {
	source := `function greet(name: string): string {
    return "hello " + name;
}

function add(a: number, b: number): number {
    return a + b;
}`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	assertSymbol(t, symbols, "greet", Function)
	assertSymbol(t, symbols, "add", Function)

	if len(symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(symbols))
	}
}

func TestTSExtractSymbols_Class(t *testing.T) {
	source := `class UserService {
    private db: Database;

    constructor(db: Database) {
        this.db = db;
    }

    getUser(id: string): User {
        return this.db.find(id);
    }

    deleteUser(id: string): void {
        this.db.delete(id);
    }
}`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	assertSymbol(t, symbols, "UserService", Class)
	assertSymbol(t, symbols, "UserService.constructor", Method)
	assertSymbol(t, symbols, "UserService.getUser", Method)
	assertSymbol(t, symbols, "UserService.deleteUser", Method)
}

func TestTSExtractSymbols_Variables(t *testing.T) {
	source := `const API_URL = "https://api.example.com";
let counter = 0;
var legacy = true;

const fetchData = async (url: string) => {
    return fetch(url);
};

const helper = function(x: number) {
    return x * 2;
};`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	assertSymbol(t, symbols, "API_URL", Variable)
	assertSymbol(t, symbols, "counter", Variable)
	assertSymbol(t, symbols, "legacy", Variable)
	assertSymbol(t, symbols, "fetchData", Function)  // arrow function -> Function kind
	assertSymbol(t, symbols, "helper", Variable)     // function expression is not detected as Function
}

func TestTSExtractSymbols_Interface(t *testing.T) {
	source := `interface User {
    id: string;
    name: string;
    email: string;
}

interface Repository<T> {
    find(id: string): T;
    save(entity: T): void;
}`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	assertSymbol(t, symbols, "User", Interface)
	assertSymbol(t, symbols, "Repository", Interface)

	if len(symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(symbols))
	}
}

func TestTSExtractSymbols_TypeAlias(t *testing.T) {
	source := `type UserID = string;
type Result<T> = { data: T; error: string | null };
type Handler = (req: Request, res: Response) => void;`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	assertSymbol(t, symbols, "UserID", TypeAlias)
	assertSymbol(t, symbols, "Result", TypeAlias)
	assertSymbol(t, symbols, "Handler", TypeAlias)
}

func TestTSExtractSymbols_Enum(t *testing.T) {
	source := `enum Direction {
    Up = "UP",
    Down = "DOWN",
    Left = "LEFT",
    Right = "RIGHT",
}

enum Status {
    Active,
    Inactive,
    Pending,
}`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	assertSymbol(t, symbols, "Direction", Enum)
	assertSymbol(t, symbols, "Status", Enum)
}

func TestTSExtractSymbols_Exported(t *testing.T) {
	source := `export function publicFunc(): void {}

export class PublicClass {
    method(): void {}
}

export const PUBLIC_CONST = 42;

export interface PublicInterface {
    field: string;
}

export type PublicType = string;

export enum PublicEnum { A, B }

function privateFunc(): void {}
const privateConst = "secret";`
	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	// Exported symbols should have ExportName set
	for _, s := range symbols {
		switch s.Name {
		case "publicFunc", "PublicClass", "PUBLIC_CONST", "PublicInterface", "PublicType", "PublicEnum":
			if s.ExportName == "" {
				t.Errorf("symbol %q should be exported but ExportName is empty", s.Name)
			}
		case "privateFunc", "privateConst":
			if s.ExportName != "" {
				t.Errorf("symbol %q should not be exported but ExportName=%q", s.Name, s.ExportName)
			}
		}
	}

	assertSymbol(t, symbols, "publicFunc", Function)
	assertSymbol(t, symbols, "PublicClass", Class)
	assertSymbol(t, symbols, "PUBLIC_CONST", Variable)
	assertSymbol(t, symbols, "PublicInterface", Interface)
	assertSymbol(t, symbols, "PublicType", TypeAlias)
	assertSymbol(t, symbols, "PublicEnum", Enum)
	assertSymbol(t, symbols, "privateFunc", Function)
	assertSymbol(t, symbols, "privateConst", Variable)
}

// --- Import extraction ---

func TestTSExtractImports_Named(t *testing.T) {
	source := `import { useState, useEffect } from 'react';
import { UserService } from './services/user';`
	h := &TSHandler{}
	root := parseTS(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	if imports[0].Source != "react" {
		t.Errorf("import 0: expected source %q, got %q", "react", imports[0].Source)
	}
	if len(imports[0].Specifiers) != 2 {
		t.Errorf("import 0: expected 2 specifiers, got %d", len(imports[0].Specifiers))
	}
	if imports[0].IsDefault {
		t.Error("import 0: should not be default")
	}

	if imports[1].Source != "./services/user" {
		t.Errorf("import 1: expected source %q, got %q", "./services/user", imports[1].Source)
	}
}

func TestTSExtractImports_Default(t *testing.T) {
	source := `import React from 'react';
import express from 'express';`
	h := &TSHandler{}
	root := parseTS(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	if imports[0].Source != "react" {
		t.Errorf("import 0: expected source %q, got %q", "react", imports[0].Source)
	}
	if !imports[0].IsDefault {
		t.Error("import 0: should be default")
	}
	if len(imports[0].Specifiers) != 1 || imports[0].Specifiers[0] != "React" {
		t.Errorf("import 0: expected specifier [React], got %v", imports[0].Specifiers)
	}
}

func TestTSExtractImports_Namespace(t *testing.T) {
	source := `import * as path from 'path';
import * as utils from './utils';`
	h := &TSHandler{}
	root := parseTS(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(imports))
	}

	if !imports[0].IsNamespace {
		t.Error("import 0: should be namespace")
	}
	if imports[0].Source != "path" {
		t.Errorf("import 0: expected source %q, got %q", "path", imports[0].Source)
	}
	if !imports[1].IsNamespace {
		t.Error("import 1: should be namespace")
	}
}

func TestTSExtractImports_Mixed(t *testing.T) {
	source := `import React, { useState, useEffect } from 'react';`
	h := &TSHandler{}
	root := parseTS(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}

	imp := imports[0]
	if imp.Source != "react" {
		t.Errorf("expected source %q, got %q", "react", imp.Source)
	}
	if !imp.IsDefault {
		t.Error("should be default (has default specifier)")
	}
	// Should have React + useState + useEffect
	if len(imp.Specifiers) != 3 {
		t.Errorf("expected 3 specifiers, got %d: %v", len(imp.Specifiers), imp.Specifiers)
	}
}

func TestTSExtractImports_Aliased(t *testing.T) {
	source := `import { Component as Comp, Injectable as Inj } from '@angular/core';`
	h := &TSHandler{}
	root := parseTS(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}

	// Aliases should be used as specifiers
	imp := imports[0]
	found := map[string]bool{}
	for _, s := range imp.Specifiers {
		found[s] = true
	}
	if !found["Comp"] {
		t.Errorf("expected alias 'Comp' in specifiers, got %v", imp.Specifiers)
	}
	if !found["Inj"] {
		t.Errorf("expected alias 'Inj' in specifiers, got %v", imp.Specifiers)
	}
}

// --- Full file ---

func TestTSExtractSymbols_FullFile(t *testing.T) {
	source := `import { Database } from './db';
import type { Config } from './config';

export interface UserDTO {
    id: string;
    name: string;
}

export type UserID = string;

export enum Role {
    Admin = "ADMIN",
    User = "USER",
}

export class UserService {
    private db: Database;

    constructor(db: Database) {
        this.db = db;
    }

    async getUser(id: UserID): Promise<UserDTO> {
        return this.db.find(id);
    }

    async deleteUser(id: UserID): Promise<void> {
        await this.db.delete(id);
    }
}

export function createService(db: Database): UserService {
    return new UserService(db);
}

export const DEFAULT_LIMIT = 100;

const internalHelper = (x: number): number => x * 2;`

	h := &TSHandler{}
	root := parseTS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.ts")

	if len(symbols) < 9 {
		t.Errorf("expected at least 9 symbols, got %d", len(symbols))
		for _, s := range symbols {
			t.Logf("  %s %s (%s:%d-%d)", s.Kind, s.Name, s.FilePath, s.StartLine, s.EndLine)
		}
	}

	assertSymbol(t, symbols, "UserDTO", Interface)
	assertSymbol(t, symbols, "UserID", TypeAlias)
	assertSymbol(t, symbols, "Role", Enum)
	assertSymbol(t, symbols, "UserService", Class)
	assertSymbol(t, symbols, "UserService.constructor", Method)
	assertSymbol(t, symbols, "UserService.getUser", Method)
	assertSymbol(t, symbols, "UserService.deleteUser", Method)
	assertSymbol(t, symbols, "createService", Function)
	assertSymbol(t, symbols, "DEFAULT_LIMIT", Variable)
	assertSymbol(t, symbols, "internalHelper", Function)

	// Verify imports
	imports := h.ExtractImports(root, []byte(source))
	if len(imports) != 2 {
		t.Errorf("expected 2 imports, got %d", len(imports))
	}
}

// --- JavaScript compatibility ---

func TestTSExtractSymbols_JavaScript(t *testing.T) {
	source := `const API_KEY = "abc123";

function fetchUsers() {
    return fetch("/users");
}

class ApiClient {
    request(url) {
        return fetch(url);
    }
}

const processData = (data) => {
    return data.map(d => d.id);
};`

	h := &TSHandler{}
	root := parseJS(t, source)
	symbols := h.ExtractSymbols(root, []byte(source), "test.js")

	assertSymbol(t, symbols, "API_KEY", Variable)
	assertSymbol(t, symbols, "fetchUsers", Function)
	assertSymbol(t, symbols, "ApiClient", Class)
	assertSymbol(t, symbols, "ApiClient.request", Method)
	assertSymbol(t, symbols, "processData", Function)
}

func TestTSExtractImports_JavaScript(t *testing.T) {
	source := `import express from 'express';
import { Router } from 'express';
import * as fs from 'fs';`

	h := &TSHandler{}
	root := parseJS(t, source)
	imports := h.ExtractImports(root, []byte(source))

	if len(imports) != 3 {
		t.Fatalf("expected 3 imports, got %d", len(imports))
	}

	if imports[0].Source != "express" || !imports[0].IsDefault {
		t.Errorf("import 0: expected default import from 'express', got %+v", imports[0])
	}
	if imports[1].Source != "express" || imports[1].IsDefault {
		t.Errorf("import 1: expected named import from 'express', got %+v", imports[1])
	}
	if imports[2].Source != "fs" || !imports[2].IsNamespace {
		t.Errorf("import 2: expected namespace import from 'fs', got %+v", imports[2])
	}
}
