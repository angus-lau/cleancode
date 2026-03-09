package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/angus-lau/cleancode/internal/query"
	"github.com/fsnotify/fsnotify"
)

var sourceExts = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".go": true,
}

var ignoreDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true,
	".git": true, "coverage": true, ".cleancode": true,
	"__pycache__": true, "venv": true, ".venv": true, "vendor": true,
}

type Watcher struct {
	rootPath string
	engine   *query.Engine
	fsw      *fsnotify.Watcher

	// Debounce: collect changed files, index once per batch
	mu       sync.Mutex
	pending  map[string]bool
	timer    *time.Timer
	debounce time.Duration
}

func New(rootPath string, engine *query.Engine) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	return &Watcher{
		rootPath: rootPath,
		engine:   engine,
		fsw:      fsw,
		pending:  make(map[string]bool),
		debounce: 500 * time.Millisecond,
	}, nil
}

// Watch starts watching the project directory for changes and re-indexes on file saves.
// This blocks until the context is cancelled or an error occurs.
func (w *Watcher) Watch(onIndex func(files int, symbols int, edges int, elapsed time.Duration)) error {
	// Add all directories recursively
	count, err := w.addDirs()
	if err != nil {
		return err
	}
	fmt.Printf("  Watching %d directories\n", count)

	for {
		select {
		case event, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(event, onIndex)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "  Watch error: %v\n", err)
		}
	}
}

func (w *Watcher) Close() error {
	return w.fsw.Close()
}

func (w *Watcher) handleEvent(event fsnotify.Event, onIndex func(int, int, int, time.Duration)) {
	path := event.Name

	// If a new directory was created, start watching it
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			name := filepath.Base(path)
			if !ignoreDirs[name] && !strings.HasPrefix(name, ".") {
				w.fsw.Add(path)
			}
			return
		}
	}

	// Only care about source file writes/creates/renames
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) && !event.Has(fsnotify.Remove) {
		return
	}

	ext := strings.ToLower(filepath.Ext(path))
	if !sourceExts[ext] {
		return
	}

	// Debounce: accumulate changes and index once
	w.mu.Lock()
	w.pending[path] = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(w.debounce, func() {
		w.flush(onIndex)
	})
	w.mu.Unlock()
}

func (w *Watcher) flush(onIndex func(int, int, int, time.Duration)) {
	w.mu.Lock()
	count := len(w.pending)
	w.pending = make(map[string]bool)
	w.mu.Unlock()

	if count == 0 {
		return
	}

	result, err := w.engine.Index()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Index error: %v\n", err)
		return
	}

	if onIndex != nil {
		onIndex(result.Files, result.Symbols, result.Edges, result.Elapsed)
	}
}

func (w *Watcher) addDirs() (int, error) {
	count := 0
	err := filepath.WalkDir(w.rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if ignoreDirs[name] || (strings.HasPrefix(name, ".") && path != w.rootPath) {
			return filepath.SkipDir
		}
		if err := w.fsw.Add(path); err != nil {
			return nil // Skip dirs we can't watch
		}
		count++
		return nil
	})
	return count, err
}
