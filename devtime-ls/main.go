package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"

	_ "github.com/tliron/commonlog/simple"
)

const (
	lsName            = "devtime-ls"
	heartbeatInterval = 30 * time.Second
)

var (
	projectFolder string
	projectName   string
	handler       protocol.Handler
	tracker       *Tracker
)

// Tracker deduplicates heartbeats and writes JSONL events.
type Tracker struct {
	mu       sync.Mutex
	lastURI  string
	lastTime time.Time
	langs    map[string]string // URI → language ID cache
}

func NewTracker() *Tracker {
	return &Tracker{langs: make(map[string]string)}
}

func (t *Tracker) heartbeat(uri, lang string, isWrite bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if lang != "" {
		t.langs[uri] = lang
	}

	now := time.Now()

	// Skip if same file, within interval, and not a save
	if !isWrite && uri == t.lastURI && now.Sub(t.lastTime) < heartbeatInterval {
		return
	}

	if lang == "" {
		lang = t.langs[uri]
	}

	t.writeEvent(now, lang)
	t.lastURI = uri
	t.lastTime = now
}

func (t *Tracker) writeEvent(now time.Time, lang string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".devtime")
	os.MkdirAll(dir, 0755)

	path := filepath.Join(dir, fmt.Sprintf("events-%s.jsonl", now.Format("2006-01")))

	entry := map[string]string{
		"ts":      now.Format("2006-01-02T15:04:05-07:00"),
		"event":   "heartbeat",
		"project": projectName,
		"lang":    normalizeLang(lang),
		"editor":  "zed",
	}

	data, _ := json.Marshal(entry)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}

// langMap normalizes Zed language IDs to match devtime's expected format.
var langMap = map[string]string{
	"go mod":  "go",
	"go sum":  "go",
	"go work": "go",
	"go asm":  "go",
	"gotmpl":  "go",
}

func normalizeLang(id string) string {
	lower := strings.ToLower(id)
	if n, ok := langMap[lower]; ok {
		return n
	}
	return lower
}

func main() {
	flag.StringVar(&projectFolder, "project-folder", "", "project folder path")
	flag.Parse()

	if projectFolder != "" {
		projectName = filepath.Base(projectFolder)
	}

	tracker = NewTracker()

	handler = protocol.Handler{
		Initialize:            initialize,
		Initialized:           initialized,
		Shutdown:              shutdown,
		TextDocumentDidOpen:   textDocumentDidOpen,
		TextDocumentDidChange: textDocumentDidChange,
		TextDocumentDidSave:   textDocumentDidSave,
	}

	s := server.NewServer(&handler, lsName, false)
	s.RunStdio()
}

func initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	caps := handler.CreateServerCapabilities()

	sync := protocol.TextDocumentSyncKindIncremental
	caps.TextDocumentSync = protocol.TextDocumentSyncOptions{
		OpenClose: boolPtr(true),
		Change:    &sync,
		Save: &protocol.SaveOptions{
			IncludeText: boolPtr(false),
		},
	}

	return protocol.InitializeResult{Capabilities: caps}, nil
}

func initialized(ctx *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

func shutdown(ctx *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func textDocumentDidOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	tracker.heartbeat(params.TextDocument.URI, params.TextDocument.LanguageID, false)
	return nil
}

func textDocumentDidChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	tracker.heartbeat(params.TextDocument.URI, "", false)
	return nil
}

func textDocumentDidSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	tracker.heartbeat(params.TextDocument.URI, "", true)
	return nil
}

func boolPtr(b bool) *bool {
	return &b
}
