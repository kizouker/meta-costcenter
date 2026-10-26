package costcenter

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Version 1 adapter: native envelope -> payload.event.model_completed. Only allowlisted usage
// fields reach SQLite. Transcript fields are never persisted.

const parserVersion = 1

// SessionMeta is an explicit project or billing mode for one session, from the config file.
type SessionMeta struct {
	Project     *string `toml:"project"`
	BillingMode *string `toml:"billing_mode"`
}

// ScanResult is what one scan found.
type ScanResult struct {
	Added     int      `json:"added"`
	Files     int      `json:"files"`
	Warnings  []string `json:"warnings"`
	ScannedAt string   `json:"scanned_at"`
}

// recordError is a malformed or unsupported record; it is counted, never fatal.
type recordError struct{ message string }

func (e *recordError) Error() string { return e.message }

func bad(format string, args ...any) error { return &recordError{fmt.Sprintf(format, args...)} }

func count(value any) (int64, error) {
	n, ok := value.(json.Number)
	if !ok || !isIntLiteral(n) {
		return 0, bad("invalid token count")
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || i < 0 {
		return 0, bad("invalid token count")
	}
	return i, nil
}

// tokens reads input, output, and cached counts; cached is nil when the log does not say.
func tokens(usage map[string]any) (int64, int64, *int64, error) {
	inputValue, ok := usage["input_tokens"]
	if !ok {
		return 0, 0, nil, bad("missing input_tokens")
	}
	outputValue, ok := usage["output_tokens"]
	if !ok {
		return 0, 0, nil, bad("missing output_tokens")
	}
	input, err := count(inputValue)
	if err != nil {
		return 0, 0, nil, err
	}
	output, err := count(outputValue)
	if err != nil {
		return 0, 0, nil, err
	}
	var cachedValue any
	for _, key := range []string{"cached_tokens", "cached_input_tokens", "cache_read_tokens"} {
		if v, present := usage[key]; present {
			cachedValue = v
			break
		}
	}
	if cachedValue == nil {
		return input, output, nil, nil
	}
	cached, err := count(cachedValue)
	if err != nil {
		return 0, 0, nil, err
	}
	if cached > input {
		return 0, 0, nil, bad("cached tokens exceed input")
	}
	return input, output, &cached, nil
}

// recordTimestamp converts a record's timestamp to the stored form.
func recordTimestamp(value any) (string, error) {
	switch v := value.(type) {
	case json.Number:
		if isIntLiteral(v) {
			i, err := strconv.ParseInt(string(v), 10, 64)
			if err != nil {
				return "", bad("timestamp out of range")
			}
			return timestampFromNumber(true, i, 0)
		}
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil || math.IsInf(f, 0) {
			return "", bad("timestamp out of range")
		}
		return timestampFromNumber(false, 0, f)
	case bool:
		// Python counts a boolean as the integer 0 or 1.
		if v {
			return timestampFromNumber(true, 1, 0)
		}
		return timestampFromNumber(true, 0, 0)
	case string:
		t, err := parseISO(strings.ReplaceAll(v, "Z", "+00:00"))
		if err != nil {
			return "", bad("%v", err)
		}
		return isoformat(t), nil
	}
	return "", bad("missing timestamp")
}

func fileIdentity(path string) (string, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", uint64(st.Dev), st.Ino), info.Size(), nil
}

// anchorAt identifies the record that ends at offset by normalized record metadata, never by
// transcript bytes or their hashes. A different anchor on the next scan means the file was replaced.
func anchorAt(f *os.File, offset int64) string {
	if offset == 0 {
		return ""
	}
	pos := max(0, offset-4096)
	var data []byte
	split := -1
	for {
		data = make([]byte, offset-pos)
		n, _ := f.ReadAt(data, pos)
		data = bytes.TrimRight(data[:n], "\n")
		split = bytes.LastIndexByte(data, '\n')
		if split >= 0 || pos == 0 {
			break
		}
		pos = max(0, pos-65536)
	}
	value, err := decodeSelective(data[split+1:], anchorFields)
	record, ok := value.(map[string]any)
	if err != nil || !ok {
		return "malformed-record"
	}
	return pyDumps([]any{record["id"], record["sequence"], record["recorded_at"]})
}

// pathParts splits a path as Python's PurePosixPath.parts does: the root, then each component.
func pathParts(path string) []string {
	var parts []string
	if strings.HasPrefix(path, "/") {
		parts = append(parts, "/")
	}
	for _, p := range strings.Split(path, "/") {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	return parts
}

// pathName is PurePosixPath.name: the last component, or "" for a root.
func pathName(path string) string {
	parts := pathParts(path)
	if len(parts) == 0 || parts[len(parts)-1] == "/" {
		return ""
	}
	return parts[len(parts)-1]
}

func pathStem(path string) string {
	name := pathName(path)
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// sessionInfo derives a log's session and parent session from its path.
func sessionInfo(path string) (string, string) {
	parts := pathParts(path)
	session := pathName(filepath.Dir(path))
	parent := ""
	for _, label := range []string{"subagent", "observer", "approval-review"} {
		i := -1
		for j := len(parts) - 1; j >= 0; j-- {
			if parts[j] == label {
				i = j
				break
			}
		}
		if i > 0 && i+1 < len(parts) {
			parent = parts[i-1]
			if label == "approval-review" {
				session = pathStem(path)
			} else {
				session = parts[i+1]
			}
		}
	}
	return session, parent
}

type sourceState struct {
	identity, anchor string
	offset, errors   int64
}

// child is record.get(key, {}) for a JSON object: missing gives an empty object, and a value
// that is not an object is a malformed record.
func child(m map[string]any, key string) (map[string]any, error) {
	value, present := m[key]
	if !present {
		return map[string]any{}, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, bad("%s is not an object", key)
	}
	return object, nil
}

func stringValue(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

// Ingest reads the new complete lines of one log, returning how many events it added.
func (s *Store) Ingest(path, project, billingMode string) (int, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	source := resolved
	var previous *sourceState
	var row sourceState
	err = s.DB.QueryRow("SELECT COALESCE(identity, ''), COALESCE(offset, 0), COALESCE(anchor, ''), COALESCE(errors, 0) FROM sources WHERE path=?", source).
		Scan(&row.identity, &row.offset, &row.anchor, &row.errors)
	switch {
	case err == nil:
		previous = &row
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	identity, size, err := fileIdentity(resolved)
	if err != nil {
		return 0, err
	}
	session, parent := sessionInfo(resolved)
	detectedProject, detectedModel := "unassigned", "unknown"
	var metaProject, metaModel sql.NullString
	if err := s.DB.QueryRow("SELECT project, model FROM metadata WHERE source=?", source).Scan(&metaProject, &metaModel); err == nil {
		detectedProject, detectedModel = metaProject.String, metaModel.String
	}
	f, err := os.Open(resolved)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var offset int64
	if previous != nil {
		offset = previous.offset
	}
	reset := previous != nil && (previous.identity != identity || size < offset || anchorAt(f, offset) != previous.anchor)
	if reset {
		offset = 0
		detectedProject, detectedModel = "unassigned", "unknown"
	}
	// The malformed-record count survives incremental scans and resets when the file is replaced.
	var errorCount int64
	if previous != nil && !reset {
		errorCount = previous.errors
	}
	added, pending := 0, 0
	err = s.inTx(func(tx *sql.Tx) error {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		statements := &ingestStatements{tx: tx}
		defer statements.close()
		reader := bufio.NewReaderSize(f, 1<<16)
		for {
			start := offset
			line, err := reader.ReadBytes('\n')
			if len(line) == 0 && err != nil {
				break
			}
			if !bytes.HasSuffix(line, []byte("\n")) {
				pending = 1
				break
			}
			offset += int64(len(line))
			n, err := ingestLine(statements, line, start, source, session, parent, project, billingMode, &detectedProject, &detectedModel)
			var rec *recordError
			switch {
			case errors.As(err, &rec):
				errorCount++
			case err != nil:
				return err
			}
			added += n
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO metadata VALUES (?,?,?)", source, detectedProject, detectedModel); err != nil {
			return err
		}
		status := "ok"
		if reset {
			status = "replaced/truncated; historical observations retained"
		}
		_, err := tx.Exec(`INSERT INTO sources VALUES (?,?,?,?,?,?,?,?,?)
          ON CONFLICT(path) DO UPDATE SET identity=excluded.identity, offset=excluded.offset,
          anchor=excluded.anchor, scanned_at=excluded.scanned_at, errors=excluded.errors,
          pending=excluded.pending, status=excluded.status, parser_version=excluded.parser_version`,
			source, identity, offset, anchorAt(f, offset), nowISO(), errorCount, pending, status, parserVersion)
		return err
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// ingestLine stores one record. A *recordError means the record was malformed or unsupported.
func ingestLine(st *ingestStatements, line []byte, start int64, source, session, parent, project, billingMode string,
	detectedProject, detectedModel *string) (int, error) {
	value, err := decodeSelective(line, recordFields)
	if err != nil {
		return 0, bad("%v", err)
	}
	record, ok := value.(map[string]any)
	if !ok {
		return 0, bad("record is not an object")
	}
	payload, err := child(record, "payload")
	if err != nil {
		return 0, err
	}
	event, err := child(payload, "event")
	if err != nil {
		return 0, err
	}
	if record["payload_type"] == "runtime.session.metadata" {
		meta, err := child(payload, "record")
		if err != nil {
			return 0, err
		}
		if root, ok := stringValue(meta["workspace_root"]); ok {
			*detectedProject = pathName(root)
			if *detectedProject == "" {
				*detectedProject = "unassigned"
			}
		}
		if model, ok := stringValue(meta["model_id"]); ok {
			*detectedModel = model
		}
		return 0, nil
	}
	kindValue, present := event["kind"]
	if !present {
		kindValue = record["type"]
	}
	kind, _ := kindValue.(string)
	if kind != "model_completed" && kind != "run.usage" {
		return 0, nil
	}
	timestampValue, present := record["recorded_at"]
	if !present {
		timestampValue = record["timestamp"]
	}
	recordedAt, err := recordTimestamp(timestampValue)
	if err != nil {
		return 0, err
	}
	// Native IDs survive moves, truncation, and rescans. The fallback uses source and sequence.
	sequenceValue, present := record["sequence"]
	if !present {
		sequenceValue = json.Number(strconv.FormatInt(start, 10))
	}
	sequence := pyStr(sequenceValue)
	stream, err := child(record, "stream")
	if err != nil {
		return 0, err
	}
	sid := session
	if truthy(stream["id"]) {
		sid = pyStr(stream["id"])
	} else if truthy(record["session_id"]) {
		sid = pyStr(record["session_id"])
	}
	var rid string
	if truthy(record["id"]) {
		rid = pyStr(record["id"])
	} else {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s", source, sid, sequence, kind)))
		rid = hex.EncodeToString(sum[:])
	}
	if kind == "run.usage" {
		// An explicit, supported interchange shape; unknown upstream shapes are diagnosed.
		usage, err := child(record, "usage")
		if err != nil {
			return 0, err
		}
		models, ok := usage["models"].([]any)
		if !ok || len(models) == 0 {
			return 0, bad("unsupported run.usage shape")
		}
		for _, item := range models {
			m, ok := item.(map[string]any)
			if !ok {
				return 0, bad("unsupported run.usage shape")
			}
			input, output, cached, err := tokens(m)
			if err != nil {
				return 0, err
			}
			modelValue, present := m["model"]
			if !present {
				return 0, bad("missing model")
			}
			model := pyStr(modelValue)
			insert, err := st.prepare(&st.summary, "INSERT OR IGNORE INTO summaries VALUES (?,?,?,?,?,?,?,?)")
			if err != nil {
				return 0, err
			}
			if _, err := insert.Exec(rid+":"+model, sid, recordedAt, model, input, output, nullableInt(cached), source); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}
	usageValue, present := event["usage"]
	if !present {
		return 0, bad("missing usage")
	}
	usage, ok := usageValue.(map[string]any)
	if !ok {
		return 0, bad("usage is not an object")
	}
	input, output, cached, err := tokens(usage)
	if err != nil {
		return 0, err
	}
	model := *detectedModel
	if truthy(event["model"]) {
		model = pyStr(event["model"])
	}
	status := "cache_unknown"
	if cached != nil {
		status = "complete"
	}
	eventProject := project
	if project == "unassigned" {
		eventProject = *detectedProject
	}
	insert, err := st.prepare(&st.event, "INSERT OR IGNORE INTO events (id, session, parent, project, recorded_at, model, billing_mode, input, output, "+
		"cached, source, sequence, usage_status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return 0, err
	}
	result, err := insert.Exec(rid, sid, nullString(parent), eventProject, recordedAt, model, billingMode, input, output, nullableInt(cached),
		source, sequence, status)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// ingestStatements prepares each insert once per file rather than once per record.
type ingestStatements struct {
	tx             *sql.Tx
	event, summary *sql.Stmt
}

func (st *ingestStatements) prepare(slot **sql.Stmt, query string) (*sql.Stmt, error) {
	if *slot == nil {
		stmt, err := st.tx.Prepare(query)
		if err != nil {
			return nil, err
		}
		*slot = stmt
	}
	return *slot, nil
}

func (st *ingestStatements) close() {
	for _, stmt := range []*sql.Stmt{st.event, st.summary} {
		if stmt != nil {
			stmt.Close()
		}
	}
}

func nullableInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// discover finds session logs under a root: session.jsonl files and approval reviews.
func discover(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{filepath.Clean(root)}, nil
	}
	var paths []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // an unreadable subdirectory is skipped, as pathlib's glob does
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if d.Name() == "session.jsonl" || containsPart(path, "approval-review") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func containsPart(path, part string) bool {
	for _, p := range pathParts(path) {
		if p == part {
			return true
		}
	}
	return false
}

// lessPath orders paths component by component, as Python sorts Path objects.
func lessPath(a, b string) bool {
	pa, pb := pathParts(a), pathParts(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// Scan reads every log under the roots and applies explicit session metadata from the config.
func (s *Store) Scan(roots []string, sessions map[string]SessionMeta) (ScanResult, error) {
	found := map[string]bool{}
	warnings := []string{}
	for _, root := range roots {
		root = expandHome(root)
		if _, err := os.Stat(root); err != nil {
			warnings = append(warnings, "Source unavailable: "+root)
			continue
		}
		paths, err := discover(root)
		for _, p := range paths {
			found[p] = true
		}
		if err != nil {
			warnings = append(warnings, "Cannot discover source: "+root)
		}
	}
	paths := make([]string, 0, len(found))
	for p := range found {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return lessPath(paths[i], paths[j]) })
	added := 0
	for _, path := range paths {
		session, _ := sessionInfo(path)
		project, billingMode := "unassigned", "unknown"
		if meta, ok := sessions[session]; ok {
			if meta.Project != nil {
				project = *meta.Project
			}
			if meta.BillingMode != nil {
				billingMode = *meta.BillingMode
			}
		}
		n, err := s.Ingest(path, project, billingMode)
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			warnings = append(warnings, "Cannot read source: "+path)
			continue
		}
		if err != nil {
			return ScanResult{}, err
		}
		added += n
	}
	// Detected billing modes are applied first so that explicit metadata mappings, which also apply to
	// previously imported observations, override them.
	err := s.inTx(func(tx *sql.Tx) error {
		if err := applyDetections(tx, sessions); err != nil {
			return err
		}
		for sid, meta := range sessions {
			if meta.Project != nil {
				if _, err := tx.Exec("UPDATE events SET project=? WHERE session=?", *meta.Project, sid); err != nil {
					return err
				}
			}
			if meta.BillingMode != nil {
				if _, err := tx.Exec("UPDATE events SET billing_mode=? WHERE session=?", *meta.BillingMode, sid); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}
	if len(paths) == 0 {
		warnings = append(warnings, "No session logs discovered; coverage unavailable (not zero usage).")
	}
	rows, err := s.DB.Query("SELECT path FROM sources")
	if err != nil {
		return ScanResult{}, err
	}
	missing := 0
	for rows.Next() {
		var path string
		rows.Scan(&path)
		if !found[path] {
			missing++
		}
	}
	rows.Close()
	if missing > 0 {
		warnings = append(warnings, fmt.Sprintf("%d previously imported sources outside this scan or no longer available; "+
			"retained usage remains visible.", missing))
	}
	return ScanResult{Added: added, Files: len(paths), Warnings: warnings, ScannedAt: nowISO()}, nil
}
