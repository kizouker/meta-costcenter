package costcenter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeSecret = "SECRET_KEY_VALUE"

// fakeProc is a /proc with a shell (pid 100) started by Muse (pid 50).
func fakeProc(t *testing.T, environ, cmdline []string) {
	t.Helper()
	root := t.TempDir()
	write := func(pid, name, text string) {
		if err := os.MkdirAll(filepath.Join(root, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pid, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100", "stat", "100 (sh) S 50 100 100 0")
	write("50", "stat", "50 (muse-bin-1.4.13) S 1 50 50 0")
	write("50", "environ", strings.Join(environ, "\x00")+"\x00")
	write("50", "cmdline", strings.Join(cmdline, "\x00")+"\x00")
	saved := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = saved })
}

// authFile writes Muse credentials under a new config home and returns the XDG_CONFIG_HOME entry for it.
func authFile(t *testing.T, meta map[string]any) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "muse", "auth.json")
	os.MkdirAll(filepath.Dir(path), 0o700)
	if meta != nil {
		data, _ := json.Marshal(map[string]any{"schema_version": 1, "providers": map[string]any{"meta": meta}})
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return "XDG_CONFIG_HOME=" + home
}

func TestDetectFollowsMusePrecedence(t *testing.T) {
	login := map[string]any{"mechanism": "oauth", "access_token": fakeSecret, "api_key": fakeSecret, "subs_active": true}
	stored := map[string]any{"api_key": fakeSecret}
	cases := []struct {
		name    string
		environ []string
		cmdline []string
		auth    map[string]any
		want    Detection
	}{
		{"environment key with Keyguard label", []string{"HOME=/h", "META_API_KEY=" + fakeSecret, "META_KEYGUARD_KEY=day-zero-test"},
			nil, login, Detection{BillingMode: "payg", Source: "env_key", KeyLabel: "day-zero-test"}},
		{"environment key without Keyguard", []string{"META_API_KEY=" + fakeSecret}, nil, login,
			Detection{BillingMode: "payg", Source: "env_key"}},
		{"empty environment key is no key", []string{"META_API_KEY="}, nil, login,
			Detection{BillingMode: "subscription", Source: "account_login"}},
		{"key on stdin", nil, []string{"muse", "exec", "--api-key-stdin"}, login, Detection{BillingMode: "payg", Source: "stdin_key"}},
		{"key stored with muse auth", nil, nil, stored, Detection{BillingMode: "payg", Source: "stored_key"}},
		{"account login with a subscription", nil, nil, login, Detection{BillingMode: "subscription", Source: "account_login"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeProc(t, append(c.environ, authFile(t, c.auth)), c.cmdline)
			pid, err := museProcess(100)
			if err != nil || pid != 50 {
				t.Fatalf("muse process %d, %v", pid, err)
			}
			if got := detect(pid); got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
	for name, auth := range map[string]map[string]any{"no credentials": nil,
		"login without a subscription": {"mechanism": "oauth", "api_key": fakeSecret, "subs_active": false}} {
		fakeProc(t, []string{authFile(t, auth)}, nil)
		got := detect(50)
		if got.BillingMode != "unknown" || got.Source != "undetermined" || got.Detail == "" {
			t.Fatalf("%s: %+v", name, got)
		}
		if strings.Contains(got.Detail, fakeSecret) {
			t.Fatalf("%s: detail holds the secret", name)
		}
	}
	if _, err := museProcess(1); err == nil {
		t.Fatal("found Muse above init")
	}
}

func TestHookRecordsWithoutSecretsOrOutput(t *testing.T) {
	f := newFixture(t)
	// The test binary is not run by Muse, so the hook records an undetermined detection.
	var stderr bytes.Buffer
	HookSessionStart(strings.NewReader(`{"session_id":"parent","source":"startup","hook_event_name":"SessionStart"}`),
		filepath.Join(f.root, "db.sqlite3"), &stderr)
	detections, err := f.store.SessionDetections("parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].BillingMode != "unknown" || *detections[0].HookSource != "startup" {
		t.Fatalf("detections %+v, stderr %q", detections, stderr.String())
	}
	HookSessionStart(strings.NewReader(`{"session_id":"../../etc"}`), filepath.Join(f.root, "db.sqlite3"), &stderr)
	if !strings.Contains(stderr.String(), "no usable session_id") {
		t.Fatalf("stderr %q", stderr.String())
	}
	fakeProc(t, []string{"META_API_KEY=" + fakeSecret}, nil)
	if err := f.store.RecordDetection("child", "startup", detect(50)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.dump(), fakeSecret) {
		t.Fatal("a secret reached the database")
	}
}

func (f *fixture) detection(session, at, mode, label string) {
	f.t.Helper()
	f.exec("INSERT INTO session_billing (session, detected_at, billing_mode, source, key_label) VALUES (?, ?, ?, 'env_key', ?)",
		session, at, mode, nullString(label))
}

func modes(rows []Row) map[string]string {
	result := map[string]string{}
	for _, r := range rows {
		label := ""
		if r.KeyLabel != nil {
			label = "/" + *r.KeyLabel
		}
		result[r.Session] = r.BillingMode + label
	}
	return result
}

func TestScanAppliesDetections(t *testing.T) {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	f.write(event("child-event", "child", 50, 20, "test-model"), filepath.Join(f.logs, "parent/subagent/child/session.jsonl"))
	f.scan(nil)
	if got := modes(f.rows()); got["parent"] != "unknown" || got["child"] != "unknown" {
		t.Fatalf("before detection %v", got)
	}
	// A detection after the events still covers them, because the hook fires before the first model call.
	f.detection("parent", isoformat(time.Now().Add(time.Hour)), "payg", "day-zero-test")
	f.scan(nil)
	if got := modes(f.rows()); got["parent"] != "payg/day-zero-test" || got["child"] != "payg/day-zero-test" {
		t.Fatalf("subagent should inherit the parent's detection: %v", got)
	}
	// A session resumed under a different credential: events take the latest detection before them.
	f.detection("parent", isoformat(time.Now().Add(-time.Hour)), "subscription", "")
	f.scan(nil)
	if got := modes(f.rows()); got["parent"] != "subscription" {
		t.Fatalf("after resume %v", got)
	}
	// An undetermined detection never overrides a known one.
	f.detection("parent", isoformat(time.Now().Add(-time.Minute)), "unknown", "")
	// A subagent's own detection wins over its parent's, and the config file wins over both.
	f.detection("child", isoformat(time.Now().Add(-time.Hour)), "payg", "")
	f.scan(map[string]SessionMeta{"parent": {BillingMode: str("payg")}})
	if got := modes(f.rows()); got["parent"] != "payg" || got["child"] != "payg" {
		t.Fatalf("with config %v", got)
	}
}

func TestPaygAlertCountsDetectedUsage(t *testing.T) {
	f := pricingFixture(t)
	if _, err := f.store.CreateAlert(jsonBody(t, `{"period":"day","billing_mode":"payg","limit_usd":"1"}`)); err != nil {
		t.Fatal(err)
	}
	before := f.alerts(f.rates)[0]
	if before.SpentUSD.Sign() != 0 || before.UnknownBillingEvents == 0 {
		t.Fatalf("before detection %+v", before)
	}
	for _, r := range f.rows() {
		f.detection(r.Session, "2000-01-01T00:00:00+00:00", "payg", "")
	}
	f.scan(nil)
	after := f.alerts(f.rates)[0]
	if after.SpentUSD.Sign() <= 0 || after.UnknownBillingEvents != 0 {
		t.Fatalf("after detection %+v", after)
	}
}

func settingsFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestInstallHookKeepsOtherSettings(t *testing.T) {
	original := `{"schema_version": 1, "model": "spark", "reasoning_effort": "high", "hooks": {"SessionStart": [
  {"matcher": "startup", "hooks": [{"type": "command", "command": "notify-send started"}]}],
  "Stop": [{"hooks": [{"type": "command", "command": "echo stop"}]}]}}`
	path := settingsFile(t, original)
	command := "/usr/local/bin/meta-cost hook session-start"
	changed, err := InstallHook(path, command)
	if err != nil || !changed {
		t.Fatalf("install %v %v", changed, err)
	}
	if backup, _ := os.ReadFile(path + ".meta-cost.bak"); string(backup) != original {
		t.Fatal("backup does not hold the original file")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %v", info.Mode().Perm())
	}
	settings := readJSON(t, path)
	if settings["model"] != "spark" || settings["schema_version"] != float64(1) {
		t.Fatalf("settings lost: %v", settings)
	}
	hooks := settings["hooks"].(map[string]any)
	if len(hooks["SessionStart"].([]any)) != 2 || len(hooks["Stop"].([]any)) != 1 {
		t.Fatalf("hooks %v", hooks)
	}
	if changed, err := InstallHook(path, command); err != nil || changed {
		t.Fatalf("second install %v %v", changed, err)
	}
	// A moved binary updates the existing entry rather than adding a second one.
	if changed, err := InstallHook(path, "/home/me/go/bin/meta-cost hook session-start"); err != nil || !changed {
		t.Fatalf("reinstall %v %v", changed, err)
	}
	if groups := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any); len(groups) != 2 {
		t.Fatalf("groups after reinstall %v", groups)
	}
	if changed, err := UninstallHook(path); err != nil || !changed {
		t.Fatalf("uninstall %v %v", changed, err)
	}
	after := readJSON(t, path)
	var want map[string]any
	json.Unmarshal([]byte(original), &want)
	if a, _ := json.Marshal(after); string(a) != string(mustJSON(want)) {
		t.Fatalf("uninstall left %s", a)
	}
	if changed, err := UninstallHook(path); err != nil || changed {
		t.Fatalf("second uninstall %v %v", changed, err)
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func TestInstallHookIntoSettingsWithoutHooks(t *testing.T) {
	path := settingsFile(t, `{"schema_version": 1}`)
	if _, err := InstallHook(path, "meta-cost hook session-start"); err != nil {
		t.Fatal(err)
	}
	if changed, err := UninstallHook(path); err != nil || !changed {
		t.Fatalf("uninstall %v %v", changed, err)
	}
	if after := readJSON(t, path); len(after) != 1 {
		t.Fatalf("empty hooks table left behind: %v", after)
	}
	if _, err := InstallHook(filepath.Join(t.TempDir(), "settings.json"), "x"); err == nil ||
		!strings.Contains(err.Error(), "run Muse once") {
		t.Fatalf("missing settings: %v", err)
	}
	if _, err := InstallHook(settingsFile(t, `{"hooks": []}`), "x"); err == nil {
		t.Fatal("accepted a hooks list")
	}
}

func TestBillingDiagnosticsAndBackfill(t *testing.T) {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	f.scan(nil)
	settings := settingsFile(t, `{"schema_version": 1}`)
	d, err := f.store.BillingDiagnostics(settings, nil)
	if err != nil || d.HookInstalled || d.Warning == nil || !strings.Contains(*d.Warning, "hook install") {
		t.Fatalf("not installed: %+v %v", d, err)
	}
	InstallHook(settings, "/bin/meta-cost hook session-start")
	// A detection from before the session, for another session: the hook was running, yet this session has none.
	f.detection("earlier", "2000-01-01T00:00:00+00:00", "payg", "")
	d, _ = f.store.BillingDiagnostics(settings, nil)
	if !d.HookInstalled || d.UndetectedSessions != 1 || d.Warning == nil {
		t.Fatalf("undetected: %+v", d)
	}
	if d, _ = f.store.BillingDiagnostics(settings, map[string]SessionMeta{"parent": {BillingMode: str("payg")}}); d.UndetectedSessions != 0 {
		t.Fatalf("configured session counted: %+v", d)
	}
	tomorrow := dateString(today().AddDate(0, 0, 1))
	sessions, err := f.store.UndetectedBefore(tomorrow)
	if err != nil || len(sessions) != 1 || sessions[0].Session != "parent" {
		t.Fatalf("undetected %+v %v", sessions, err)
	}
	if none, _ := f.store.UndetectedBefore(dateString(today())); len(none) != 0 {
		t.Fatalf("before today %+v", none)
	}
	if err := f.store.Backfill(sessions, "subscription"); err != nil {
		t.Fatal(err)
	}
	f.scan(nil)
	if got := modes(f.rows()); got["parent"] != "subscription" {
		t.Fatalf("after backfill %v", got)
	}
}

func TestAuthPathFollowsTheMuseProcess(t *testing.T) {
	for env, want := range map[museEnv]string{
		{configHome: "/cfg", home: "/home/me"}: "/cfg/muse/auth.json",
		{home: "/home/me"}:                     "/home/me/.config/muse/auth.json",
		{configHome: "relative", home: "/h"}:   "/h/.config/muse/auth.json",
	} {
		if got := env.authPath(); got != want {
			t.Fatalf("%+v: %s, want %s", env, got, want)
		}
	}
	command, err := hookCommandLine("~/data/costs.sqlite3", "/etc/meta cost.toml")
	if err != nil || !strings.Contains(command, " hook session-start --db /") ||
		!strings.Contains(command, "--config '/etc/meta cost.toml'") || strings.Contains(command, "~") {
		t.Fatalf("command %q, %v", command, err)
	}
	if !isOurHook("/home/me/.local/bin/meta-cost hook session-start --db /d --config /c") || isOurHook("echo hook session-start") {
		t.Fatal("isOurHook")
	}
}
