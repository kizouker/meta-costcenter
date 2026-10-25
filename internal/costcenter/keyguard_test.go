package costcenter

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeKeyguard writes a stand-in `keyguard` command that prints a fixed `status --json`.
func fakeKeyguard(t *testing.T, folder string, status map[string]any, code int) Config {
	t.Helper()
	data, _ := json.Marshal(status)
	script := filepath.Join(folder, "keyguard")
	text := fmt.Sprintf("#!/bin/sh\n[ \"$*\" = \"status --json\" ] || exit 9\ncat <<'EOF'\n%s\nEOF\nexit %d\n", data, code)
	if err := os.WriteFile(script, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
	return Config{KeyguardCommand: &script}
}

func kgStatus(changes map[string]any) map[string]any {
	status := map[string]any{"keyguard_version": "0.1.0", "bridge_running": true,
		"cost_center":  map[string]any{"enabled": true, "last_check": map[string]any{"ok": true, "at": "2026-10-25T12:00:00+00:00"}},
		"last_failure": nil,
		"keys": []any{map[string]any{"label": "bootcamp-api", "state": "active", "meta_name": "bootcamp-api-kg1",
			"expires_at": "2026-10-25T20:00:00+00:00", "overdue": false, "auto_rotate": false, "last_error": nil}},
		"targets": []any{map[string]any{"name": "bootcamp-api", "managed": true}, map[string]any{"name": "bootcamp-web", "managed": true},
			map[string]any{"name": "Muse Code", "managed": false}},
		"targets_as_of": "2026-10-25T12:00:00+00:00", "revocations": []any{}}
	for k, v := range changes {
		status[k] = v
	}
	return status
}

func revokingAlert() AlertStatus {
	return AlertStatus{ID: 1, RevokeKeys: true, KeyMatch: str("bootcamp-*"), WindowStart: dateString(today()), Level: "exceeded"}
}

func clearKeyguardCache(t *testing.T) {
	keyguardMu.Lock()
	keyguardCached = map[string]cachedStatus{}
	keyguardMu.Unlock()
	t.Cleanup(func() {
		keyguardMu.Lock()
		keyguardCached = map[string]cachedStatus{}
		keyguardMu.Unlock()
	})
}

func annotateOne(t *testing.T, config Config) map[string]any {
	t.Helper()
	annotated := AnnotateAlerts([]AlertStatus{revokingAlert()}, config)
	if len(annotated) != 1 || annotated[0].Keyguard == nil {
		t.Fatalf("not annotated: %+v", annotated)
	}
	return annotated[0].Keyguard
}

func TestAlertsWithoutKeyRevocationNeverRunKeyguard(t *testing.T) {
	clearKeyguardCache(t)
	missing := filepath.Join(t.TempDir(), "missing")
	annotated := AnnotateAlerts([]AlertStatus{{ID: 1}}, Config{KeyguardCommand: &missing})
	if annotated[0].Keyguard != nil {
		t.Fatalf("annotated %+v", annotated[0])
	}
	if len(keyguardCached) != 0 {
		t.Fatal("Keyguard was run")
	}
}

func TestEnforcedAlertListsTheKeysItCovers(t *testing.T) {
	clearKeyguardCache(t)
	info := annotateOne(t, fakeKeyguard(t, t.TempDir(), kgStatus(nil), 0))
	if fmt.Sprint(info["matching"]) != "[bootcamp-api bootcamp-web]" || info["enforced"] != true {
		t.Fatalf("%v", info)
	}
	if _, ok := info["revoked"]; ok {
		t.Fatal("revoked without revocations")
	}
}

func TestRevocationsCountOnlyInTheCurrentWindow(t *testing.T) {
	clearKeyguardCache(t)
	now := time.Now().UTC()
	config := fakeKeyguard(t, t.TempDir(), kgStatus(map[string]any{"revocations": []any{
		map[string]any{"at": isoformat(now.AddDate(0, 0, -3)), "alert_id": 1, "keys": []any{"old-key"}},
		map[string]any{"at": isoformat(now), "alert_id": 1, "keys": []any{"bootcamp-api"}},
		map[string]any{"at": isoformat(now), "alert_id": 2, "keys": []any{"other-alert"}}}}), 0)
	revoked, _ := annotateOne(t, config)["revoked"].(map[string]any)
	if fmt.Sprint(revoked["keys"]) != "[bootcamp-api]" {
		t.Fatalf("revoked %v", revoked)
	}
}

func TestReasonsAnAlertIsNotEnforced(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	for _, c := range []struct {
		config func() Config
		reason string
	}{
		{func() Config {
			return fakeKeyguard(t, dir, kgStatus(map[string]any{"cost_center": map[string]any{"enabled": false, "last_check": nil}}), 0)
		}, "check is off"},
		{func() Config {
			return fakeKeyguard(t, dir, kgStatus(map[string]any{"cost_center": map[string]any{"enabled": true,
				"last_check": map[string]any{"ok": false, "error": "boom"}}}), 0)
		}, "boom"},
		{func() Config { return fakeKeyguard(t, dir, map[string]any{}, 1) }, "Keyguard failed"},
		{func() Config { return Config{KeyguardCommand: &missing} }, "not found"},
	} {
		clearKeyguardCache(t)
		info := annotateOne(t, c.config())
		reason, _ := info["reason"].(string)
		if info["enforced"] != false || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: %v", c.reason, info)
		}
	}
}

func TestBridgeDownIsEnforcedWithAWarning(t *testing.T) {
	clearKeyguardCache(t)
	info := annotateOne(t, fakeKeyguard(t, t.TempDir(), kgStatus(map[string]any{"bridge_running": false}), 0))
	warning, _ := info["warning"].(string)
	if info["enforced"] != true || !strings.Contains(warning, "bridge is not running") {
		t.Fatalf("%v", info)
	}
}

func TestPreviewAndCache(t *testing.T) {
	clearKeyguardCache(t)
	dir := t.TempDir()
	config := fakeKeyguard(t, dir, kgStatus(nil), 0)
	preview := func(match string) string {
		summary, err := KeyguardSummary(config, &match)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(summary["preview"])
	}
	if got := preview("Muse*, bootcamp-web"); got != "[bootcamp-web Muse Code]" {
		t.Fatalf("preview %s", got)
	}
	if got := preview("nothing-*"); got != "[]" {
		t.Fatalf("preview %s", got)
	}
	bad := "bad key!"
	if _, err := KeyguardSummary(config, &bad); !isInvalid(err) {
		t.Fatalf("bad pattern: %v", err)
	}
	// A changed status is not seen until the cache expires.
	fakeKeyguard(t, dir, kgStatus(map[string]any{"targets": []any{}}), 0)
	summary, _ := KeyguardSummary(config, nil)
	if targets, _ := summary["targets"].([]any); len(targets) != 3 {
		t.Fatalf("targets %v", summary["targets"])
	}
	clock := time.Now()
	keyguardClock = func() time.Time { return clock }
	t.Cleanup(func() { keyguardClock = time.Now })
	clearKeyguardCache(t)
	KeyguardStatus(config)
	clock = clock.Add(keyguardCache + time.Second)
	if targets, _ := KeyguardStatus(config)["targets"].([]any); len(targets) != 0 {
		t.Fatalf("stale targets %v", targets)
	}
}

func TestAlertsAndDashboardCarryKeyguardState(t *testing.T) {
	clearKeyguardCache(t)
	w := newWebFixture(t, func(f *fixture) Config { return fakeKeyguard(t, f.root, kgStatus(nil), 0) })
	w.send("POST", "/api/alerts", `{"period":"day","limit_usd":"5","revoke_keys":true,"key_match":"bootcamp-api"}`, nil)
	w.send("POST", "/api/alerts", `{"period":"day","limit_usd":"5"}`, nil)
	var listed []map[string]any
	w.getJSON("/api/alerts", &listed)
	if fmt.Sprint(listed[0]["keyguard"].(map[string]any)["matching"]) != "[bootcamp-api]" {
		t.Fatalf("%v", listed[0])
	}
	if _, ok := listed[1]["keyguard"]; ok {
		t.Fatal("an alert without key revocation has Keyguard state")
	}
	var dashboard map[string]any
	w.getJSON("/api/dashboard", &dashboard)
	if dashboard["alerts"].([]any)[0].(map[string]any)["keyguard"].(map[string]any)["enforced"] != true {
		t.Fatalf("%v", dashboard["alerts"])
	}
}

func TestKeyguardEndpointPreviewsAndRejectsBadPatterns(t *testing.T) {
	clearKeyguardCache(t)
	w := newWebFixture(t, func(f *fixture) Config { return fakeKeyguard(t, f.root, kgStatus(nil), 0) })
	var summary map[string]any
	w.getJSON("/api/keyguard?match="+url.QueryEscape("bootcamp-*"), &summary)
	if summary["installed"] != true || summary["enforced"] != true || fmt.Sprint(summary["preview"]) != "[bootcamp-api bootcamp-web]" {
		t.Fatalf("%v", summary)
	}
	if summary["keys"].([]any)[0].(map[string]any)["label"] != "bootcamp-api" {
		t.Fatalf("keys %v", summary["keys"])
	}
	code, data, _ := w.get("/api/keyguard?match="+url.QueryEscape("bad key!"), nil)
	if code != 400 || !strings.Contains(string(data), "Key pattern") {
		t.Fatalf("%d %s", code, data)
	}
}
