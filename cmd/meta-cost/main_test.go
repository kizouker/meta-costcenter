package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// These tests run the built binary, as a user would.

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "meta-cost-test")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "meta-cost")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build failed: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func event(id string, input int64) string {
	data, _ := json.Marshal(map[string]any{"id": id, "stream": map[string]any{"id": "parent"}, "sequence": 1,
		"recorded_at": time.Now().UnixMicro(),
		"payload": map[string]any{"event": map[string]any{"kind": "model_completed", "model": "test-model",
			"usage":    map[string]any{"input_tokens": input, "output_tokens": 10, "cached_tokens": 20},
			"response": "SECRET_RESPONSE"}, "prompt": "SECRET_PROMPT"}})
	return string(data) + "\n"
}

type cli struct {
	t       *testing.T
	root    string
	source  string
	options []string
}

func newCLI(t *testing.T) *cli {
	root := t.TempDir()
	c := &cli{t: t, root: root, source: filepath.Join(root, "session.jsonl")}
	if err := os.WriteFile(c.source, []byte(event("event-1", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	c.options = []string{"--db", filepath.Join(root, "db.sqlite3"), "--source", c.source}
	return c
}

func (c *cli) cmd(args ...string) *exec.Cmd {
	cmd := exec.Command(binary, append(args, c.options...)...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+c.root, "XDG_DATA_HOME="+c.root)
	return cmd
}

// run returns the exit code, stdout, and stderr.
func (c *cli) run(args ...string) (int, string, string) {
	c.t.Helper()
	cmd := c.cmd(args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		c.t.Fatal(err)
	}
	return code, stdout.String(), stderr.String()
}

// ok runs a command that must succeed and returns its output.
func (c *cli) ok(args ...string) string {
	c.t.Helper()
	code, stdout, stderr := c.run(args...)
	if code != 0 {
		c.t.Fatalf("%v exited %d: %s", args, code, stderr)
	}
	return stdout
}

func (c *cli) json(target any, args ...string) {
	c.t.Helper()
	if err := json.Unmarshal([]byte(c.ok(args...)), target); err != nil {
		c.t.Fatal(err)
	}
}

func TestExportAndManualObservation(t *testing.T) {
	c := newCLI(t)
	var rows []map[string]any
	c.json(&rows, "export", "--period", "all")
	if len(rows) != 1 || rows[0]["estimated_api_equivalent_usd"] != nil {
		t.Fatalf("%v", rows)
	}
	if _, ok := rows[0]["source"]; ok {
		t.Fatal("export exposes source paths")
	}
	if c.json(&rows, "export", "--period", "all"); len(rows) != 1 {
		t.Fatalf("rescan duplicated rows: %d", len(rows))
	}
	var added []map[string]any
	c.json(&added, "reconcile", "add", "--amount", "12.34", "--currency", "CAD")
	if added[0]["amount"] != "12.34" || added[0]["currency"] != "CAD" {
		t.Fatalf("%v", added)
	}
	if !strings.Contains(c.ok("tui", "--once", "--period", "all"), "unknown") {
		t.Fatal("report does not show the unpriced model as unknown")
	}
}

func TestWarningLevelsAndInvalidFilters(t *testing.T) {
	c := newCLI(t)
	if !strings.Contains(c.ok("alert", "set", "--monthly-usd", "30", "--at", "50,80,100"), "does not stop usage") {
		t.Fatal("alert without key revocation does not say it only warns")
	}
	c.ok("alert", "set", "--monthly-usd", "0.01")
	prices := filepath.Join(c.root, "prices.json")
	// Rates high enough that the fixture's few tokens pass a one-cent limit.
	os.WriteFile(prices, []byte(`{"rates":[{"model":"test-model","currency":"USD","effective_from":"2020-01-01",
		"retrieved_at":"2020-01-01","snapshot_version":"fixture","source_url":"https://example.invalid",
		"input_per_million":"100000","cached_input_per_million":"10000","output_per_million":"200000"}]}`), 0o644)
	if !strings.Contains(c.ok("month", "--prices", prices), "WARNING: Monthly limit for all projects") {
		t.Fatal("no warning for an exceeded alert")
	}
	code, _, stderr := c.run("show", "--from", "bad-date")
	if code != 1 || !strings.HasPrefix(stderr, "meta-cost: ") || strings.Contains(stderr, "goroutine") {
		t.Fatalf("%d %q", code, stderr)
	}
}

func TestAlertStatusJSONReportsKeyRevocation(t *testing.T) {
	c := newCLI(t)
	c.ok("alert", "set", "--monthly-usd", "30", "--revoke-keys", "bootcamp-*, key1")
	c.ok("alert", "set", "--monthly-usd", "40", "--project", "site")
	var statuses []map[string]any
	c.json(&statuses, "alert", "status", "--json")
	if len(statuses) != 2 || statuses[0]["project"] != nil || statuses[1]["project"] != "site" {
		t.Fatalf("%v", statuses)
	}
	if statuses[0]["revoke_keys"] != true || statuses[0]["key_match"] != "bootcamp-*,key1" ||
		statuses[1]["revoke_keys"] != false || statuses[1]["key_match"] != nil {
		t.Fatalf("%v", statuses)
	}
	if _, ok := statuses[0]["spent_usd"].(string); !ok || statuses[0]["level"] != "ok" {
		t.Fatalf("%v", statuses[0])
	}
	for _, limit := range []string{"0.001", "0.015"} {
		if code, _, stderr := c.run("alert", "set", "--monthly-usd", limit); code != 1 || !strings.Contains(stderr, "Limit must") {
			t.Errorf("%s: %d %q", limit, code, stderr)
		}
	}
	if code, _, stderr := c.run("alert", "set", "--monthly-usd", "1", "--revoke-keys", "bad key!"); code != 1 || !strings.Contains(stderr, "Key pattern") {
		t.Errorf("%d %q", code, stderr)
	}
}

func ioctl(fd uintptr, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// openPTY opens a pseudo-terminal of the given size and returns its master and slave ends.
func openPTY(rows, cols uint16) (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	var number uint32
	if err := ioctl(master.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		master.Close()
		return nil, nil, err
	}
	if err := ioctl(master.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&number)); err != nil {
		master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	size := struct{ Row, Col, X, Y uint16 }{rows, cols, 0, 0}
	if err := ioctl(slave.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&size)); err != nil {
		master.Close()
		slave.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func TestTerminalRefreshFiltersAndExit(t *testing.T) {
	c := newCLI(t)
	master, slave, err := openPTY(36, 150)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	defer master.Close()
	cmd := c.cmd("tui", "--interval", "0.1", "--period", "all")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Env = append(cmd.Env, "TERM=xterm")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	output := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				output <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(output)
				return
			}
		}
	}()
	var seen bytes.Buffer
	readFor := func(d time.Duration) {
		deadline := time.After(d)
		for {
			select {
			case chunk, ok := <-output:
				if !ok {
					return
				}
				seen.Write(chunk)
			case <-deadline:
				return
			}
		}
	}
	readFor(400 * time.Millisecond)
	file, _ := os.OpenFile(c.source, os.O_APPEND|os.O_WRONLY, 0)
	file.WriteString(event("live-2", 500))
	file.Close()
	master.Write([]byte("r"))
	readFor(400 * time.Millisecond)
	master.Write([]byte("g"))
	readFor(200 * time.Millisecond)
	master.Write([]byte("f"))
	readFor(100 * time.Millisecond)
	if !bytes.Contains(seen.Bytes(), []byte("Period (today/week/month/all)")) {
		t.Fatalf("no filter prompt in %q", seen.String())
	}
	// Enter retains the period; blank dates, project, and model; then the parent session.
	master.Write([]byte("\n\n\n\n\nparent\n\n"))
	readFor(300 * time.Millisecond)
	master.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%v\n%s", err, seen.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the dashboard did not quit")
	}
	readFor(100 * time.Millisecond)
	if !bytes.Contains(seen.Bytes(), []byte("MUSE COST")) || !bytes.Contains(seen.Bytes(), []byte("parent")) {
		t.Fatalf("%q", seen.String())
	}
	var rows []map[string]any
	c.json(&rows, "export", "--period", "all")
	var input float64
	for _, r := range rows {
		input += r["input"].(float64)
	}
	if input != 600 {
		t.Fatalf("input %v", input)
	}
}
