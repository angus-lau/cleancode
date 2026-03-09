# cleancode

AI-powered code review CLI with deep codebase understanding. Uses tree-sitter for structural analysis, SQLite for persistent indexing, and Claude for intelligent review.

## How it works

```
cleancode index    →  Parses your codebase with tree-sitter
                      Builds a dependency graph (symbols, callers, imports)
                      Stores everything in SQLite (.cleancode/index.db)

cleancode review   →  Diffs against your base branch
                      Enriches diff with callers, dependents, DB schema
                      Runs parallel review agents via claude -p
                      Synthesizer deduplicates and prioritizes findings
```

## Install

Requires Go 1.21+, [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code), and a C compiler (for SQLite/tree-sitter).

```bash
git clone https://github.com/angus-lau/cleancode.git
cd cleancode
go build -o cleancode ./cmd/cleancode/
# Move to PATH
mv cleancode /usr/local/bin/
```

## Quick start

```bash
cd your-project

# Initialize config
cleancode init

# Build the index
cleancode index

# Review your changes
cleancode review
```

## Commands

### `cleancode init`

Creates `.cleancode.json` in your project root.

```bash
cleancode init
cleancode init --db "postgres://user:pass@localhost/mydb"  # enable schema fetching
```

### `cleancode index`

Parses the codebase, builds the dependency graph, and stores it in SQLite. Incremental — only re-parses files that changed (by content hash).

```bash
cleancode index
```

If a database URL is configured, also fetches and stores the DB schema.

### `cleancode review`

Reviews your current changes against the base branch. Automatically re-indexes before reviewing.

```bash
cleancode review                  # diff against baseBranch from config
cleancode review --base develop   # diff against specific branch
```

**What agents receive:**
- The diff (capped at 30K chars)
- Changed symbols (functions, classes modified)
- Callers of changed symbols (blast radius)
- Files that import changed files (dependents)
- DB schema for tables referenced in the diff

**Two-pass architecture:**
1. Parallel agents run independently via goroutines
2. Synthesizer deduplicates findings, resolves conflicts, and prioritizes by impact

### `cleancode search <query>`

Fuzzy search for symbols across the index. Works without re-indexing (reads from SQLite).

```bash
cleancode search authenticate
cleancode search User
```

### `cleancode callers <symbol>`

Find all call sites of a symbol. Uses precise call-site tracking (AST body walking), not just import-level edges.

```bash
cleancode callers handleLogin
cleancode callers UserService
```

### `cleancode stats`

Show index statistics.

```bash
cleancode stats
# Index Stats
#   Files:   827
#   Symbols: 26555
#   Edges:   4491
```

### `cleancode watch`

Watches for file changes and re-indexes automatically. Uses fsnotify with 500ms debounce.

```bash
cleancode watch
# Starting watch mode for /path/to/project
# Initial index: 827 files, 26555 symbols, 4491 edges (2.6s)
# Watching for changes (Ctrl+C to stop)
#   Re-indexed: 827 files, 26555 symbols, 4491 edges (31ms)
```

### `cleancode hook install|remove`

Install or remove a git pre-push hook that runs `cleancode review` automatically before every push.

```bash
cleancode hook install
cleancode hook remove
```

## Configuration

`.cleancode.json` in your project root:

```json
{
  "baseBranch": "main",
  "agents": {
    "correctness": true,
    "performance": true,
    "api-contract": true,
    "security": false
  },
  "schema": {
    "provider": "postgres",
    "url": "$DATABASE_URL"
  },
  "ignore": [
    "**/*.test.ts",
    "**/*.spec.ts",
    "**/fixtures/**"
  ]
}
```

| Field | Description |
|-------|-------------|
| `baseBranch` | Branch to diff against for reviews (default: `main`) |
| `agents` | Toggle individual review agents on/off |
| `schema.provider` | Database type (`postgres`) |
| `schema.url` | Connection string. Prefix with `$` to read from env var |
| `ignore` | Glob patterns for files to skip during indexing |

## Review agents

| Agent | Default | What it checks |
|-------|---------|----------------|
| `correctness` | on | Logic bugs, null safety, type mismatches, missing error handling |
| `performance` | on | N+1 queries, sequential awaits, unbounded queries, memory leaks |
| `api-contract` | on | Breaking changes, removed fields, changed signatures |
| `security` | off | SQL injection, auth bypass, secrets in code, OWASP top 10 |

When 2+ agents produce findings, the **synthesizer** runs a second pass to:
- Deduplicate overlapping findings
- Resolve conflicting suggestions
- Prioritize by actual impact
- Preserve original agent attribution

## Language support

| Language | File types | Symbols extracted | Imports tracked |
|----------|-----------|-------------------|-----------------|
| TypeScript | `.ts`, `.tsx` | functions, classes, methods, interfaces, types, enums, variables | `import`/`require` |
| JavaScript | `.js`, `.jsx`, `.mjs`, `.cjs` | functions, classes, methods, variables | `import`/`require` |
| Python | `.py` | functions, classes, methods, decorated functions, variables | `import`/`from...import` |
| Go | `.go` | functions, methods (with receiver), structs, interfaces, type aliases, var/const | `import` |

## Architecture

```
cmd/cleancode/main.go          CLI entry point (cobra)

internal/
  indexer/
    extractor.go                Tree-sitter parser router
    references.go               AST body walking for call-site tracking
    lang_typescript.go          TS/JS/TSX symbol + import extraction
    lang_python.go              Python symbol + import extraction
    lang_go.go                  Go symbol + import extraction
    types.go                    Symbol, ImportRef, Edge, FileNode types

  graph/graph.go                In-memory dependency graph + edge builder
  storage/store.go              SQLite persistence (files, symbols, imports, edges)
  query/engine.go               Orchestrates indexer + graph + store
  context/assembler.go          Diff parsing, context enrichment, budget formatting
  agents/
    orchestrator.go             Parallel agent runner + synthesizer
    presets.go                  Agent prompts
    types.go                   Finding, ReviewResult types
  config/config.go              .cleancode.json loading/saving
  schema/
    fetcher.go                  Postgres schema introspection
    store.go                    Schema SQLite persistence
  watcher/watcher.go            fsnotify file watcher
```

## How indexing works

1. Walk project directory, skip ignored dirs (`node_modules`, `.git`, etc.)
2. For each source file, compute content hash — skip if unchanged since last index
3. Parse with tree-sitter, extract symbols and imports via language-specific handlers
4. Walk each symbol's AST subtree to find which imported names are actually referenced (call-site tracking)
5. Resolve import paths (e.g., `./utils` → `/project/src/utils.ts`)
6. Build dependency edges: symbol A → symbol B only if A's body actually references B
7. Persist everything to SQLite (files, symbols, imports with resolved paths, edges)

## Global flags

| Flag | Description |
|------|-------------|
| `--root`, `-r` | Project root directory (default: `.`) |

## Requirements

- **Go 1.21+** for building
- **Claude Code CLI** (`claude` command) for review agents
- **C compiler** (gcc/clang) for CGo dependencies (SQLite, tree-sitter)
- **PostgreSQL** (optional) for schema fetching
