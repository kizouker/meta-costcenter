// Command live-demo writes synthetic usage every two seconds, so the dashboard can be watched
// updating. It makes no model calls and needs no credentials, so it costs nothing.
//
//	go run ./examples/live-demo [folder]
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	root := "/tmp/muse-cost-demo"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	path := filepath.Join(root, "demo-session", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	write(path, map[string]any{"payload_type": "runtime.session.metadata", "payload": map[string]any{"record": map[string]any{
		"workspace_root": "/demo/sample-project", "model_id": "muse-spark-1.3"}}})
	fmt.Printf("Synthetic logs: %s\nIn another terminal run:\nmuse-cost show --source %s --db %s --interval 1\n",
		root, root, filepath.Join(root, "demo.sqlite3"))
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00")
		write(path, map[string]any{"id": uuid(), "stream": map[string]any{"id": "demo-session"}, "recorded_at": stamp,
			"payload": map[string]any{"event": map[string]any{"kind": "model_completed", "model": "muse-spark-1.3",
				"usage": map[string]any{"input_tokens": 1200, "output_tokens": 80, "cached_tokens": 900}}}})
		fmt.Printf("%s +1,200 input / +80 output\n", stamp)
		select {
		case <-ticker.C:
		case <-interrupt:
			return
		}
	}
}

func write(path string, record any) {
	data, _ := json.Marshal(record)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}

// uuid is a random version 4 UUID.
func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
