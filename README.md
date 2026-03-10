# cleancode

AI-powered code review CLI with deep codebase understanding. Uses tree-sitter for structural analysis, SQLite for persistent indexing, and Claude for intelligent review.

## Features

### Core
| Command | What it does |
|---------|-------------|
| `cleancode index` | Tree-sitter parse → symbols, imports, edges → SQLite. Incremental by content hash. |
| `cleancode review` | Diff → enrich with callers/dependents/schema → parallel agents → synthesizer dedup |
| `cleancode search <query>` | Fuzzy symbol search across the index |
| `cleancode callers <symbol>` | Find all call sites (functions + class methods like `FollowService.batchGetFollowStates`) |
| `cleancode explain <symbol>` | AI-powered explanation with source, callers, dependents, schema context |
| `cleancode graph` | Interactive D3.js dependency graph in browser (directory clusters, focus mode, search) |
| `cleancode watch` | fsnotify auto re-index on file save |
| `cleancode stats` | File/symbol/edge counts |
| `cleancode hook install` | Git pre-push hook for automatic reviews |
| `cleancode init` | Creates `.cleancode.json` config |

### Schema Validation
Deterministic pre-check that runs before AI agents. Parses SQL and Supabase queries from your diff, extracts column references, and validates them against the indexed database schema. Catches renamed/dropped columns instantly — no LLM needed.

- Raw SQL: resolves `FROM/JOIN` aliases (e.g., `p.aura` → `posts.aura`) and validates columns
- Supabase client: validates `.select("col1, col2")`, `.order("col")`, `.gte("col", ...)`, etc.
- Suggests similar column names via Levenshtein distance (e.g., `"aura" → Did you mean "token_supply"?`)
- Filters out JS method calls (`posts.map(...)`) and common non-table prefixes

### Review Agents
4 built-in agents (correctness, performance, api-contract, security) + unlimited custom agents via config. Two-pass architecture: parallel agents → synthesizer deduplication.

### Indexing
- 5 languages: TypeScript/JavaScript, Python, Go, Swift
- Class method edge tracking (`Class.method()` calls)
- DB schema fetching (Postgres/Supabase) → included in review context
- Configurable ignore patterns

### Graph Visualization
- File-level nodes colored by directory cluster with convex hull outlines
- Click node → sidebar with symbols, imports, dependents
- `--focus` flag for 2-hop neighborhood filtering
- Search by file or symbol name

## How it works

```
cleancode index    →  Parses your codebase with tree-sitter
                      Builds a dependency graph (symbols, callers, imports)
                      Stores everything in SQLite (.cleancode/index.db)

cleancode review   →  Diffs against your base branch
                      Enriches diff with callers, dependents, DB schema
                      Schema validator checks column references (deterministic)
                      Runs parallel review agents via claude -p
                      Synthesizer deduplicates and prioritizes findings

cleancode graph    →  Interactive dependency graph in the browser
                      Files clustered by directory, colored by group
                      Click to explore symbols, imports, dependents
```

## Install

### One-liner (macOS / Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/angus-lau/cleancode/main/scripts/install.sh | bash
```

### From source

```bash
git clone https://github.com/angus-lau/cleancode.git
cd cleancode
make install
```

### Manual build

```bash
git clone https://github.com/angus-lau/cleancode.git
cd cleancode
go build -o cleancode ./cmd/cleancode/
sudo mv cleancode /usr/local/bin/
```

### Update

```bash
cd cleancode
git pull
make install
```

### Prerequisites

- **Go 1.21+** — [go.dev/dl](https://go.dev/dl/)
- **Claude Code CLI** — [docs.anthropic.com](https://docs.anthropic.com/en/docs/claude-code) (required for `review` and `explain`)
- **C compiler** — Xcode CLI tools (macOS: `xcode-select --install`) or gcc (Linux: `sudo apt install gcc`)

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

If a database URL is configured, also fetches and stores the DB schema (table names, columns, types, nullability, defaults). Schema is persisted in SQLite so subsequent reviews use it without re-fetching.

**Supabase note:** Use the session mode pooler URL (port 5432 with `postgres.{project_ref}` username), not the transaction mode pooler (port 6543) which times out on `information_schema` queries.

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

**Three-pass architecture:**
1. Schema validator checks SQL/Supabase column references against DB schema (deterministic, instant)
2. Parallel agents run independently via goroutines
3. Synthesizer deduplicates findings, resolves conflicts, and prioritizes by impact

### `cleancode explain <symbol>`

AI-powered explanation of any symbol in your codebase. Gathers the source code, callers, dependents, and referenced DB tables, then asks Claude to explain it.

```bash
cleancode explain handleLogin
cleancode explain UserService
cleancode explain fetchTransactions
```

Output covers: what it does, how it works, who uses it, side effects, and edge cases.

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

### `cleancode graph`

Opens an interactive dependency graph in the browser. File-level view with nodes colored by directory, clustered by top-level folder.

```bash
cleancode graph                           # full graph
cleancode graph --focus service.ts        # focus on a file (2-hop neighborhood)
cleancode graph --focus handleLogin       # focus on a symbol's file
```

**Features:**
- Nodes sized by connection count, colored by directory
- Directory clusters with labels and dashed outlines
- Click a node to see its symbols, imports, and dependents in a sidebar
- Click connections in sidebar to navigate to that file
- Search by file name or symbol name (press `/` to focus)
- Zoom, pan, drag nodes
- Click background to reset view

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
  "customAgents": [
    {
      "name": "compliance",
      "prompt": "You are a compliance reviewer. Check for PII exposure, GDPR violations, and data retention issues.\n\nDo NOT flag: style issues or non-compliance concerns.\nOnly flag actual compliance risks."
    }
  ],
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
| `agents` | Toggle built-in review agents on/off |
| `customAgents` | Define your own review agents with custom prompts |
| `schema.provider` | Database type (`postgres`) |
| `schema.url` | Connection string (direct or `$ENV_VAR`). For Supabase, use session mode pooler on port 5432 |
| `ignore` | Glob patterns for files to skip during indexing |

### Custom agents

Add any number of custom review agents. They run alongside built-in agents and go through the synthesizer:

```json
{
  "customAgents": [
    {
      "name": "accessibility",
      "prompt": "You are an accessibility reviewer. Check for missing ARIA labels, keyboard navigation issues, and color contrast problems."
    },
    {
      "name": "error-handling",
      "prompt": "You are an error handling reviewer. Check for uncaught exceptions, missing try/catch blocks, and error messages that leak implementation details."
    }
  ]
}
```

## Review agents

### Built-in agents

| Agent | Default | What it checks |
|-------|---------|----------------|
| `schema-check` | on (auto) | Deterministic column validation — no LLM, runs before agents |
| `correctness` | on | Logic bugs, null safety, type mismatches, missing error handling |
| `performance` | on | N+1 queries, sequential awaits, unbounded queries, memory leaks |
| `api-contract` | on | Breaking changes, removed fields, changed signatures |
| `security` | off | SQL injection, auth bypass, secrets in code, OWASP top 10 |

### Synthesizer (pass 3)

When 2+ agents produce findings, the synthesizer automatically:
- Deduplicates overlapping findings
- Resolves conflicting suggestions
- Prioritizes by actual impact
- Preserves original agent attribution

## Language support

| Language | File types | Symbols extracted | Imports tracked |
|----------|-----------|-------------------|-----------------|
| TypeScript | `.ts`, `.tsx` | functions, classes, methods, interfaces, types, enums, variables | `import`/`require` |
| JavaScript | `.js`, `.jsx`, `.mjs`, `.cjs` | functions, classes, methods, variables | `import`/`require` |
| Python | `.py` | functions, classes, methods, decorated functions, variables | `import`/`from...import` |
| Go | `.go` | functions, methods (with receiver), structs, interfaces, type aliases, var/const | `import` |
| Swift | `.swift` | classes, structs, enums (with cases), protocols, methods, properties, functions, typealiases, extensions (members prefixed with type name) | `import` |

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
    lang_swift.go               Swift symbol + import extraction
    types.go                    Symbol, ImportRef, Edge, FileNode types

  graph/graph.go                In-memory dependency graph + edge builder
  storage/store.go              SQLite persistence (files, symbols, imports, edges)
  query/engine.go               Orchestrates indexer + graph + store
  context/assembler.go          Diff parsing, context enrichment, budget formatting
  agents/
    orchestrator.go             Parallel agent runner + synthesizer
    explain.go                  AI symbol explanation via claude -p
    presets.go                  Built-in agent prompts
    types.go                    Finding, ReviewResult types
  config/config.go              .cleancode.json loading/saving
  visualizer/graph.go           Interactive D3.js dependency graph
  schema/
    fetcher.go                  Postgres schema introspection
    store.go                    Schema SQLite persistence
    validator.go                Deterministic SQL/Supabase column validation against DB schema
  watcher/watcher.go            fsnotify file watcher
```

## How indexing works

1. Walk project directory, skip ignored dirs (`node_modules`, `.git`, etc.)
2. For each source file, compute content hash — skip if unchanged since last index
3. Parse with tree-sitter, extract symbols and imports via language-specific handlers
4. Walk each symbol's AST subtree to find which imported names are actually referenced (call-site tracking)
5. For class method calls (e.g., `FollowService.batchGetFollowStates()`), track both the class reference and the `Class.method` compound reference
6. Resolve import paths (e.g., `./utils` → `/project/src/utils.ts`)
7. Build dependency edges: symbol A → symbol B only if A's body actually references B (includes class method edges)
8. Persist everything to SQLite (files, symbols, imports with resolved paths, edges)

## Global flags

| Flag | Description |
|------|-------------|
| `--root`, `-r` | Project root directory (default: `.`) |
