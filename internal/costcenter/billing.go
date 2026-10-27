package costcenter

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Billing mode detection. Muse does not log which credential a session used, so a Muse
// SessionStart hook, `meta-cost hook session-start`, records it in session_billing and each scan
// copies it onto the session's events. See plans/BILLING-MODE-DETECTION.md.

// Detection is what the hook found about one session's credential. It never holds a secret.
type Detection struct {
	BillingMode string // payg, subscription, or unknown
	Source      string // env_key, stdin_key, stored_key, account_login, or undetermined
	KeyLabel    string // the Meta Keyguard key, when META_KEYGUARD_KEY was set
	Detail      string // why the mode is unknown
}

const hookDeadline = 2 * time.Second

// hookBusyMS is how long the hook waits for a scan's write lock before giving up on the detection.
const hookBusyMS = 1500

// procRoot is replaceable in tests.
var procRoot = "/proc"

func undetermined(format string, args ...any) Detection {
	return Detection{BillingMode: "unknown", Source: "undetermined", Detail: fmt.Sprintf(format, args...)}
}

// museProcess is the nearest ancestor, starting at pid, whose command name begins with "muse".
// The hook's parent may be a shell that Muse started rather than Muse itself.
func museProcess(pid int) (int, error) {
	for depth := 0; pid > 1 && depth < 32; depth++ {
		stat, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
		if err != nil {
			return 0, err
		}
		// The command name is in parentheses and may itself contain spaces or parentheses.
		open, close := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
		if open < 0 || close < open {
			return 0, fmt.Errorf("unreadable process status for %d", pid)
		}
		if strings.HasPrefix(string(stat[open+1:close]), "muse") {
			return pid, nil
		}
		fields := strings.Fields(string(stat[close+1:]))
		if len(fields) < 2 {
			return 0, fmt.Errorf("unreadable process status for %d", pid)
		}
		if pid, err = strconv.Atoi(fields[1]); err != nil {
			return 0, err
		}
	}
	return 0, errors.New("no Muse process among the hook's ancestors")
}

// museEnv is what the hook needs from the Muse process's environment. Muse removes META_API_KEY
// and the XDG variables from the hook's own environment, so this reads the Muse process's.
type museEnv struct {
	hasKey     bool   // a non-empty META_API_KEY; its value is only tested for emptiness and kept nowhere
	label      string // META_KEYGUARD_KEY
	configHome string // XDG_CONFIG_HOME
	home       string // HOME
}

func processEnv(pid int) (museEnv, error) {
	var env museEnv
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ"))
	if err != nil {
		return env, err
	}
	// getenv returns the first match, so later duplicates are ignored.
	seen := map[string]bool{}
	for _, entry := range bytes.Split(data, []byte{0}) {
		name, value, ok := bytes.Cut(entry, []byte("="))
		if !ok || seen[string(name)] {
			continue
		}
		switch string(name) {
		case "META_API_KEY":
			env.hasKey = len(value) > 0
		case "META_KEYGUARD_KEY":
			env.label = cleanLabel(string(value))
		case "XDG_CONFIG_HOME":
			env.configHome = string(value)
		case "HOME":
			env.home = string(value)
		default:
			continue
		}
		seen[string(name)] = true
	}
	return env, nil
}

// authPath is the auth.json that this Muse process reads.
func (e museEnv) authPath() string {
	switch {
	case filepath.IsAbs(e.configHome):
		return filepath.Join(e.configHome, "muse", "auth.json")
	case filepath.IsAbs(e.home):
		return filepath.Join(e.home, ".config", "muse", "auth.json")
	}
	return filepath.Join(museConfigDir(), "auth.json")
}

// cleanLabel keeps a key label only if it is short printable text, since it ends up in the dashboard.
func cleanLabel(label string) string {
	label = strings.TrimSpace(label)
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 200 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return ""
	}
	return label
}

// stdinKey reports whether Muse was started with --api-key-stdin, which supplies an API key.
func stdinKey(pid int) bool {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	for _, arg := range bytes.Split(data, []byte{0}) {
		if string(arg) == "--api-key-stdin" {
			return true
		}
	}
	return false
}

// present records only whether a JSON string is non-empty, so a secret is never decoded into a value.
type present bool

func (p *present) UnmarshalJSON(data []byte) error {
	*p = len(data) > 2 && data[0] == '"'
	return nil
}

// museAuth is the non-secret part of Muse's auth.json. The account login writes mechanism "oauth"
// with subs_active; `muse auth set` writes only api_key.
type museAuth struct {
	Providers map[string]struct {
		Mechanism  *string `json:"mechanism"`
		SubsActive *bool   `json:"subs_active"`
		APIKey     present `json:"api_key"`
	} `json:"providers"`
}

func museConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "muse")
}

// detect applies the rules of plans/BILLING-MODE-DETECTION.md §4.2 to the Muse process, in
// Muse's own order of precedence: environment key, stdin key, stored key, account login.
func detect(musePID int) Detection {
	env, err := processEnv(musePID)
	if err != nil {
		return undetermined("cannot read the Muse process environment: %v", err)
	}
	if env.hasKey {
		return Detection{BillingMode: "payg", Source: "env_key", KeyLabel: env.label}
	}
	if stdinKey(musePID) {
		return Detection{BillingMode: "payg", Source: "stdin_key"}
	}
	data, err := os.ReadFile(env.authPath())
	if err != nil {
		return undetermined("cannot read Muse credentials: %v", err)
	}
	var auth museAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return undetermined("cannot parse Muse credentials")
	}
	meta, ok := auth.Providers["meta"]
	switch {
	case !ok:
		return undetermined("no Meta credential in Muse's auth.json")
	case meta.Mechanism == nil && bool(meta.APIKey):
		return Detection{BillingMode: "payg", Source: "stored_key"}
	case meta.Mechanism != nil && *meta.Mechanism == "oauth" && meta.SubsActive != nil && *meta.SubsActive:
		return Detection{BillingMode: "subscription", Source: "account_login"}
	}
	return undetermined("the Meta credential is neither an API key nor an active subscription")
}

// hookInput is the part of Muse's SessionStart payload the hook uses.
type hookInput struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source"`
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// recordSessionStart reads the SessionStart payload, detects the credential, and stores it.
func recordSessionStart(stdin io.Reader, dbPath string) error {
	var input hookInput
	data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &input); err != nil {
			return fmt.Errorf("unreadable SessionStart payload")
		}
	}
	if input.SessionID == "" {
		input.SessionID = os.Getenv("MUSE_SESSION_ID")
	}
	if !sessionIDPattern.MatchString(input.SessionID) {
		return fmt.Errorf("SessionStart payload has no usable session_id")
	}
	var d Detection
	if pid, err := museProcess(os.Getppid()); err != nil {
		d = undetermined("%v", err)
	} else {
		d = detect(pid)
	}
	store, err := openStore(dbPath, hookBusyMS)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.RecordDetection(input.SessionID, input.Source, d)
}

// HookSessionStart is `meta-cost hook session-start`. It must never get in Muse's way, so it gives
// up after hookDeadline, reports problems only on stderr, and prints nothing on stdout, which Muse
// may add to the model's context.
func HookSessionStart(stdin io.Reader, dbPath string, stderr io.Writer) {
	done := make(chan error, 1)
	go func() { done <- recordSessionStart(stdin, dbPath) }()
	select {
	case err := <-done:
		if err != nil {
			fmt.Fprintf(stderr, "meta-cost hook: billing mode not recorded: %v\n", err)
		}
	case <-time.After(hookDeadline):
		fmt.Fprintln(stderr, "meta-cost hook: billing mode not recorded: gave up waiting for the database")
	}
}

func (s *Store) RecordDetection(session, hookSource string, d Detection) error {
	_, err := s.DB.Exec("INSERT OR IGNORE INTO session_billing (session, detected_at, billing_mode, source, key_label, "+
		"hook_source, detail) VALUES (?, ?, ?, ?, ?, ?, ?)", session, nowISO(), d.BillingMode, d.Source,
		nullString(d.KeyLabel), nullString(hookSource), nullString(d.Detail))
	return err
}

// applyDetections copies detected billing modes onto events. Each event takes the latest detection
// at or before it, or the session's first detection for events before any, since the hook fires
// before the first model call. Subagents take the nearest ancestor's detection when they have none.
// Sessions with a billing_mode in the config are left to the config, which is applied afterwards.
func applyDetections(tx *sql.Tx, sessions map[string]SessionMeta) error {
	detected := map[string]bool{}
	rows, err := tx.Query("SELECT DISTINCT session FROM session_billing WHERE billing_mode != 'unknown'")
	if err != nil {
		return err
	}
	for rows.Next() {
		var session string
		rows.Scan(&session)
		detected[session] = true
	}
	rows.Close()
	if len(detected) == 0 {
		return nil
	}
	parents := parentMap{}
	rows, err = tx.Query("SELECT session, MAX(parent) FROM events GROUP BY session")
	if err != nil {
		return err
	}
	for rows.Next() {
		var session, parent sql.NullString
		rows.Scan(&session, &parent)
		parents[session.String] = parent.String
	}
	rows.Close()
	if _, err := tx.Exec("CREATE TEMP TABLE IF NOT EXISTS billing_map (session TEXT PRIMARY KEY, detected TEXT)"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM temp.billing_map"); err != nil {
		return err
	}
	for session := range parents {
		if meta, ok := sessions[session]; ok && meta.BillingMode != nil {
			continue
		}
		node, seen := session, map[string]bool{}
		for node != "" && !detected[node] && !seen[node] {
			seen[node] = true
			node = parents[node]
		}
		if detected[node] {
			if _, err := tx.Exec("INSERT INTO temp.billing_map VALUES (?, ?)", session, node); err != nil {
				return err
			}
		}
	}
	// Only rows that change are written, so a scan with nothing new leaves the database alone.
	_, err = tx.Exec(`WITH chosen AS (
  SELECT e.id AS id, (SELECT b.rowid FROM session_billing b WHERE b.session = m.detected AND b.billing_mode != 'unknown'
    ORDER BY b.detected_at <= e.recorded_at DESC,
      CASE WHEN b.detected_at <= e.recorded_at THEN b.detected_at END DESC, b.detected_at LIMIT 1) AS detection
  FROM events e JOIN temp.billing_map m ON m.session = e.session)
UPDATE events SET billing_mode = b.billing_mode, key_label = b.key_label
FROM chosen c JOIN session_billing b ON b.rowid = c.detection
WHERE events.id = c.id AND (events.billing_mode IS NOT b.billing_mode OR events.key_label IS NOT b.key_label)`)
	return err
}

// DetectionRow is one stored detection, as the session page and `hook status` show it.
type DetectionRow struct {
	Session     string  `json:"session"`
	DetectedAt  string  `json:"detected_at"`
	BillingMode string  `json:"billing_mode"`
	Source      string  `json:"source"`
	KeyLabel    *string `json:"key_label"`
	HookSource  *string `json:"hook_source"`
	Detail      *string `json:"detail"`
}

func (s *Store) detections(where string, args ...any) ([]DetectionRow, error) {
	rows, err := s.DB.Query("SELECT session, detected_at, billing_mode, source, key_label, hook_source, detail "+
		"FROM session_billing "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DetectionRow{}
	for rows.Next() {
		var d DetectionRow
		var label, hookSource, detail nullText
		if err := rows.Scan(&d.Session, &d.DetectedAt, &d.BillingMode, &d.Source, &label, &hookSource, &detail); err != nil {
			return nil, err
		}
		d.KeyLabel, d.HookSource, d.Detail = label.ptr(), hookSource.ptr(), detail.ptr()
		result = append(result, d)
	}
	return result, rows.Err()
}

// SessionDetections are a session's detections, oldest first.
func (s *Store) SessionDetections(session string) ([]DetectionRow, error) {
	return s.detections("WHERE session = ? ORDER BY detected_at", session)
}

// Muse settings. The hook goes in the user settings file, which applies to every workspace,
// because Muse ignores project hooks in untrusted workspaces.

// museHooks are the Muse hook events this tool installs and the subcommand each one runs.
var museHooks = []struct{ event, args string }{
	{"SessionStart", "hook session-start"},
	{"UserPromptSubmit", "hook prompt-submit"},
}

func museSettingsPath() string { return filepath.Join(museConfigDir(), "settings.json") }

// hookCommandLine is the command the hook runs: this binary by absolute path, so that the hook
// works when the binary's directory is not on Muse's PATH, with the database and config named
// explicitly, because Muse runs hooks without XDG_DATA_HOME or XDG_CONFIG_HOME.
func hookCommandLine(args, dbPath, configPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	for _, p := range []*string{&dbPath, &configPath} {
		if *p, err = filepath.Abs(expandHome(*p)); err != nil {
			return "", err
		}
	}
	return shellWord(exe) + " " + args + " --db " + shellWord(dbPath) + " --config " + shellWord(configPath), nil
}

func shellWord(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/._-+:@%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isOurHook recognises an entry this tool installed for one hook subcommand, including one pointing
// at an older binary path. With args empty it recognises any of them.
func isOurHook(command, args string) bool {
	if !strings.Contains(command, "meta-cost") {
		return false
	}
	for _, h := range museHooks {
		if (args == "" || args == h.args) && strings.Contains(command, " "+h.args) {
			return true
		}
	}
	return false
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Muse settings not found at %s; run Muse once, then try again", path)
		}
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var settings map[string]any
	if err := decoder.Decode(&settings); err != nil || settings == nil {
		return nil, fmt.Errorf("Muse settings at %s are not a JSON object", path)
	}
	return settings, nil
}

// hookGroups is settings.hooks[event], or an error if the file uses another shape.
func hookGroups(settings map[string]any, event string) ([]any, error) {
	hooks, ok := settings["hooks"]
	if !ok || hooks == nil {
		return nil, nil
	}
	table, ok := hooks.(map[string]any)
	if !ok {
		return nil, errors.New("Muse settings have a hooks value that is not an object")
	}
	groups, ok := table[event]
	if !ok || groups == nil {
		return nil, nil
	}
	list, ok := groups.([]any)
	if !ok {
		return nil, fmt.Errorf("Muse settings have a hooks.%s value that is not a list", event)
	}
	return list, nil
}

// eachHook calls fn for every command hook in one event's groups.
func eachHook(groups []any, fn func(entry map[string]any, command string)) {
	for _, group := range groups {
		g, _ := group.(map[string]any)
		entries, _ := g["hooks"].([]any)
		for _, entry := range entries {
			e, _ := entry.(map[string]any)
			if command, ok := e["command"].(string); ok {
				fn(e, command)
			}
		}
	}
}

// writeSettings replaces the settings file atomically, keeping its permissions. Before the first
// change it keeps a copy beside it, named for this tool so that it never overwrites Muse's own backups.
func writeSettings(path string, settings map[string]any) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	backup := path + ".meta-cost.bak"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(backup, original, info.Mode().Perm()); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(buf.Bytes()); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// InstallHook adds this tool's hooks to Muse's settings, commands giving the command for each event,
// or points existing ones at those commands. It keeps every other setting and hook, and reports
// whether the file changed.
func InstallHook(path string, commands map[string]string) (bool, error) {
	settings, err := readSettings(path)
	if err != nil {
		return false, err
	}
	changed := false
	for _, h := range museHooks {
		command, ok := commands[h.event]
		if !ok {
			continue
		}
		groups, err := hookGroups(settings, h.event)
		if err != nil {
			return false, err
		}
		found := false
		eachHook(groups, func(entry map[string]any, existing string) {
			if isOurHook(existing, h.args) {
				found = true
				if existing != command {
					entry["command"], changed = command, true
				}
			}
		})
		if !found {
			groups = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
			table, _ := settings["hooks"].(map[string]any)
			if table == nil {
				table = map[string]any{}
				settings["hooks"] = table
			}
			table[h.event] = groups
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, writeSettings(path, settings)
}

// UninstallHook removes only the entries InstallHook added, and any groups or tables left empty.
func UninstallHook(path string) (bool, error) {
	settings, err := readSettings(path)
	if err != nil {
		return false, err
	}
	changed := false
	for _, h := range museHooks {
		groups, err := hookGroups(settings, h.event)
		if err != nil {
			return false, err
		}
		removed := false
		var kept []any
		for _, group := range groups {
			g, ok := group.(map[string]any)
			entries, isList := g["hooks"].([]any)
			if !ok || !isList {
				kept = append(kept, group)
				continue
			}
			var remaining []any
			for _, entry := range entries {
				if e, _ := entry.(map[string]any); e != nil {
					if command, _ := e["command"].(string); isOurHook(command, h.args) {
						removed = true
						continue
					}
				}
				remaining = append(remaining, entry)
			}
			// A group that held only this tool's hooks goes too; an unrelated empty group stays.
			if len(remaining) > 0 {
				g["hooks"] = remaining
				kept = append(kept, g)
			} else if len(entries) == 0 {
				kept = append(kept, g)
			}
		}
		if !removed {
			continue
		}
		changed = true
		table := settings["hooks"].(map[string]any)
		if len(kept) > 0 {
			table[h.event] = kept
		} else {
			delete(table, h.event)
		}
		if len(table) == 0 {
			delete(settings, "hooks")
		}
	}
	if !changed {
		return false, nil
	}
	return true, writeSettings(path, settings)
}

// BillingDiagnostics is what `doctor` and `hook status` report about billing mode detection.
type BillingDiagnostics struct {
	MuseSettings  string  `json:"muse_settings"`
	HookInstalled bool    `json:"hook_installed"`
	HookCommand   *string `json:"hook_command"`
	// PromptHookInstalled is the UserPromptSubmit hook, which records the effort of each turn.
	PromptHookInstalled bool    `json:"prompt_hook_installed"`
	LastDetectedAt      *string `json:"last_detected_at"`
	Detections          int     `json:"detections"`
	UndetectedSessions  int     `json:"undetected_sessions_7d"`
	Warning             *string `json:"warning"`
}

// BillingDiagnostics reports whether the hook is installed and whether it appears to be running. A
// session that started after the first detection but has none of its own suggests that a Muse
// update stopped running hooks.
func (s *Store) BillingDiagnostics(settingsPath string, sessions map[string]SessionMeta) (BillingDiagnostics, error) {
	d := BillingDiagnostics{MuseSettings: settingsPath}
	var warning string
	if settings, err := readSettings(settingsPath); err != nil {
		warning = err.Error()
	} else {
		for _, h := range museHooks {
			groups, err := hookGroups(settings, h.event)
			if err != nil {
				warning = err.Error()
				break
			}
			eachHook(groups, func(_ map[string]any, command string) {
				switch {
				case !isOurHook(command, h.args):
				case h.event == "SessionStart":
					c := command
					d.HookInstalled, d.HookCommand = true, &c
				default:
					d.PromptHookInstalled = true
				}
			})
		}
	}
	var last, first sql.NullString
	if err := s.DB.QueryRow("SELECT MAX(detected_at), MIN(CASE WHEN source != 'backfill' THEN detected_at END), "+
		"COUNT(*) FROM session_billing").Scan(&last, &first, &d.Detections); err != nil {
		return d, err
	}
	if last.Valid {
		d.LastDetectedAt = &last.String
	}
	if first.Valid {
		rows, err := s.DB.Query("SELECT session FROM events WHERE parent IS NULL GROUP BY session "+
			"HAVING MIN(recorded_at) >= ? AND MAX(recorded_at) >= ? "+
			"AND session NOT IN (SELECT session FROM session_billing)", first.String, isoformat(now().AddDate(0, 0, -7)))
		if err != nil {
			return d, err
		}
		for rows.Next() {
			var session string
			rows.Scan(&session)
			if meta, ok := sessions[session]; !ok || meta.BillingMode == nil {
				d.UndetectedSessions++
			}
		}
		rows.Close()
	}
	switch {
	case warning != "":
	case !d.HookInstalled:
		warning = "Billing mode detection is off, so new sessions are recorded as unknown. Run `meta-cost hook install`."
	case d.UndetectedSessions > 0:
		warning = fmt.Sprintf("%d session(s) in the last 7 days have no billing mode detection; "+
			"the Muse hook may have stopped running.", d.UndetectedSessions)
	case !d.PromptHookInstalled:
		warning = "The effort of each turn is not recorded. Run `meta-cost hook install` again to add the prompt hook."
	}
	if warning != "" {
		d.Warning = &warning
	}
	return d, nil
}

// UndetectedSession is a top-level session with no detection, for `billing backfill`.
type UndetectedSession struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Started string `json:"started"`
}

// UndetectedBefore lists top-level sessions with no detection that started before the local date.
func (s *Store) UndetectedBefore(before string) ([]UndetectedSession, error) {
	rows, err := s.DB.Query("SELECT session, MAX(project), MIN(recorded_at) FROM events WHERE parent IS NULL " +
		"AND session NOT IN (SELECT session FROM session_billing) GROUP BY session ORDER BY MIN(recorded_at)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []UndetectedSession{}
	for rows.Next() {
		var u UndetectedSession
		var project sql.NullString
		if err := rows.Scan(&u.Session, &project, &u.Started); err != nil {
			return nil, err
		}
		u.Project = project.String
		if localDay(u.Started) < before {
			result = append(result, u)
		}
	}
	return result, rows.Err()
}

// Backfill records a billing mode for sessions the hook never saw. Each detection is dated at the
// session's first event so that it covers all of the session's usage.
func (s *Store) Backfill(sessions []UndetectedSession, mode string) error {
	return s.inTx(func(tx *sql.Tx) error {
		for _, u := range sessions {
			if _, err := tx.Exec("INSERT OR IGNORE INTO session_billing (session, detected_at, billing_mode, source) "+
				"VALUES (?, ?, ?, 'backfill')", u.Session, u.Started, mode); err != nil {
				return err
			}
		}
		return nil
	})
}
