package costcenter

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// `meta-cost record` adds usage that Muse never logs, such as a script calling the Meta API
// directly. Recorded usage is stored like Muse usage, so it appears under its project and
// session and counts toward alerts, with source "record:<tool>" so that it can be told apart.

// Recording is one API call reported by a script.
type Recording struct {
	ID          string `json:"id"`
	Tool        string `json:"tool"`
	Project     string `json:"project"`
	Session     string `json:"session"`
	Model       string `json:"model"`
	BillingMode string `json:"billing_mode"`
	KeyLabel    string `json:"key_label,omitempty"`
	RecordedAt  string `json:"recorded_at"`
	Input       int64  `json:"input"`
	Output      int64  `json:"output"`
	Cached      int64  `json:"cached"`
	Images      *int64 `json:"images,omitempty"`
}

var recordIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// workspaceName is the project a recording belongs to when none is given: the name of the git
// repository holding dir, or of dir itself, as Muse names a session's workspace.
func workspaceName(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return filepath.Base(d)
		}
		if filepath.Dir(d) == d {
			return filepath.Base(dir)
		}
	}
}

func shortText(name, value string) error {
	if value == "" || utf8.RuneCountInString(value) > 200 {
		return fmt.Errorf("%s must be a name of at most 200 characters", name)
	}
	return nil
}

// newRecording validates a recording from the command's options, filling in the defaults.
func newRecording(a args) (Recording, error) {
	r := Recording{Tool: a.get("tool"), Project: a.get("project"), Session: a.get("session"), Model: a.get("model"),
		BillingMode: a.get("billing_mode"), KeyLabel: cleanLabel(a.get("key")), ID: a.get("id")}
	if r.Tool == "" {
		r.Tool = "script"
	}
	if r.Project == "" {
		dir, err := os.Getwd()
		if err != nil {
			return r, err
		}
		r.Project = workspaceName(dir)
	}
	if r.BillingMode == "" {
		// A script calling the API directly pays with an API key.
		r.BillingMode = "payg"
	}
	if !a.has("key") {
		r.KeyLabel = cleanLabel(os.Getenv("META_KEYGUARD_KEY"))
	}
	moment := now()
	if a.has("at") {
		t, err := parseISO(a.get("at"))
		if err != nil {
			return r, fmt.Errorf("--at must be an ISO 8601 time with a timezone")
		}
		moment = t
	}
	r.RecordedAt = isoformat(moment)
	if r.Session == "" {
		// One session per tool and local day keeps the Sessions page readable.
		r.Session = r.Tool + "-" + moment.Local().Format("2006-01-02")
	}
	if r.ID == "" {
		random := make([]byte, 12)
		rand.Read(random)
		r.ID = "record-" + hex.EncodeToString(random)
	}
	for _, check := range []struct{ name, value string }{{"--tool", r.Tool}, {"--project", r.Project}, {"--session", r.Session}, {"--model", r.Model}} {
		if err := shortText(check.name, check.value); err != nil {
			return r, err
		}
	}
	if !recordIDPattern.MatchString(r.ID) {
		return r, fmt.Errorf("--id must be 1 to 128 letters, digits, or ._:-")
	}
	counts := []struct {
		name string
		into *int64
	}{{"input_tokens", &r.Input}, {"output_tokens", &r.Output}, {"cached_tokens", &r.Cached}}
	for _, c := range counts {
		if !a.has(c.name) {
			continue
		}
		n, err := strconv.ParseInt(a.get(c.name), 10, 64)
		if err != nil || n < 0 {
			return r, fmt.Errorf("--%s must be a whole number of at least 0", flagName(c.name))
		}
		*c.into = n
	}
	if a.has("images") {
		n, err := strconv.ParseInt(a.get("images"), 10, 64)
		if err != nil || n < 0 {
			return r, fmt.Errorf("--images must be a whole number of at least 0")
		}
		r.Images = &n
	}
	if r.Cached > r.Input {
		return r, fmt.Errorf("--cached-tokens cannot exceed --input-tokens")
	}
	return r, nil
}

func flagName(dest string) string {
	b := []byte(dest)
	for i := range b {
		if b[i] == '_' {
			b[i] = '-'
		}
	}
	return string(b)
}

// Record stores a recording. Recording the same ID again changes nothing, so a script can retry.
func (s *Store) Record(r Recording) (bool, error) {
	var images any
	if r.Images != nil {
		images = *r.Images
	}
	result, err := s.DB.Exec("INSERT OR IGNORE INTO events (id, session, parent, project, recorded_at, model, billing_mode, input, output, "+
		"cached, source, sequence, usage_status, key_label, turn, images) VALUES (?,?,NULL,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		r.ID, r.Session, r.Project, r.RecordedAt, r.Model, r.BillingMode, r.Input, r.Output, r.Cached,
		"record:"+r.Tool, r.ID, "complete", nullString(r.KeyLabel), r.ID, images)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}
