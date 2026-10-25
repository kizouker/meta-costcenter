package costcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"
)

// What Meta Keyguard is doing about alerts that revoke API keys.
//
// Cost Center never changes keys itself. It reads `keyguard status --json`, which holds no
// secrets, to show whether those alerts are enforced, which keys they cover, and what Keyguard
// has revoked. Keyguard is optional: without it, these alerts are shown as not enforced.

// The dashboard polls every two seconds, so Keyguard's answer is reused for a short while.
const (
	keyguardCache        = 10 * time.Second
	keyguardMissingCache = 60 * time.Second
	keyguardTimeout      = 10 * time.Second
)

type cachedStatus struct {
	until  time.Time
	status map[string]any
}

var (
	keyguardMu     sync.Mutex
	keyguardCached = map[string]cachedStatus{}
	// keyguardClock is replaceable in tests.
	keyguardClock = time.Now
)

func keyguardCommand(config Config) []string {
	command := "keyguard"
	if config.KeyguardCommand != nil {
		command = *config.KeyguardCommand
	}
	return splitCommand(command)
}

// KeyguardStatus is Keyguard's status, or {"available": false, ...} explaining why it could not be read.
func KeyguardStatus(config Config) map[string]any {
	argv := keyguardCommand(config)
	key := strings.Join(argv, "\x00")
	keyguardMu.Lock()
	defer keyguardMu.Unlock()
	if cached, ok := keyguardCached[key]; ok && keyguardClock().Before(cached.until) {
		return cached.status
	}
	status := readKeyguard(argv)
	ttl := keyguardMissingCache
	if status["available"] == true || status["installed"] == true {
		ttl = keyguardCache
	}
	keyguardCached[key] = cachedStatus{keyguardClock().Add(ttl), status}
	return status
}

func readKeyguard(argv []string) map[string]any {
	if len(argv) == 0 {
		return map[string]any{"available": false, "installed": false,
			"error": "Meta Keyguard was not found (). Set keyguard_command in the Cost Center config."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), keyguardTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], "status", "--json")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return map[string]any{"available": false, "installed": false,
			"error": fmt.Sprintf("Meta Keyguard was not found (%s). Set keyguard_command in the Cost Center config.", argv[0])}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return map[string]any{"available": false, "installed": true, "error": "Meta Keyguard did not answer in time."}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output := strings.TrimSpace(stderr.String())
		if output == "" {
			output = strings.TrimSpace(stdout.String())
		}
		last := fmt.Sprint(exitErr.ExitCode())
		if output != "" {
			lines := strings.Split(output, "\n")
			last = lines[len(lines)-1]
		}
		return map[string]any{"available": false, "installed": true, "error": "Meta Keyguard failed: " + last}
	}
	if err != nil {
		return map[string]any{"available": false, "installed": false, "error": "Meta Keyguard could not be run: " + err.Error()}
	}
	var data map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil || data == nil {
		return map[string]any{"available": false, "installed": true, "error": "Meta Keyguard is too old to report its status; update it."}
	}
	data["available"], data["installed"] = true, true
	return data
}

func object(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

// enforcement says whether Keyguard will act on alerts that revoke keys, and if not, why.
func enforcement(kg map[string]any) map[string]any {
	if kg["available"] != true {
		return map[string]any{"enforced": false, "reason": kg["error"]}
	}
	costCenter := object(kg["cost_center"])
	if costCenter["enabled"] != true {
		return map[string]any{"enforced": false, "reason": "Keyguard's Cost Center check is off. Run `keyguard cost-center on`."}
	}
	if last := object(costCenter["last_check"]); len(last) > 0 {
		if ok, present := last["ok"]; present && !truthy(ok) {
			return map[string]any{"enforced": false, "reason": fmt.Sprintf("Keyguard cannot run Cost Center: %s", pyStr(last["error"]))}
		}
	}
	if !truthy(kg["bridge_running"]) {
		return map[string]any{"enforced": true, "warning": "Keyguard's bridge is not running, so keys cannot be deleted until " +
			"Chrome is open with the Keyguard extension."}
	}
	return map[string]any{"enforced": true}
}

// matching is the current keys a key match covers, by Keyguard label or dev.meta.ai name.
func matching(kg map[string]any, keyMatch any) ([]string, error) {
	patterns, err := KeyPatterns(keyMatch)
	if err != nil {
		return nil, err
	}
	names := []string{}
	targets, _ := kg["targets"].([]any)
	for _, t := range targets {
		name, _ := object(t)["name"].(string)
		for _, p := range strings.Split(patterns, ",") {
			// Patterns hold no '/' or '[', so path.Match behaves as Python's fnmatchcase here.
			if ok, _ := path.Match(p, name); ok {
				names = append(names, name)
				break
			}
		}
	}
	return names, nil
}

// AnnotateAlerts adds Keyguard's view to every alert that revokes keys. Others are left untouched.
func AnnotateAlerts(alerts []AlertStatus, config Config) []AlertStatus {
	revoking := false
	for _, a := range alerts {
		revoking = revoking || a.RevokeKeys
	}
	if !revoking {
		return alerts
	}
	kg := KeyguardStatus(config)
	state := enforcement(kg)
	for i := range alerts {
		a := &alerts[i]
		if !a.RevokeKeys {
			continue
		}
		info := map[string]any{}
		for k, v := range state {
			info[k] = v
		}
		if kg["available"] == true {
			// Only revocations in the alert's current window count; a new day or month starts clean.
			var at string
			var keys []string
			seen := map[string]bool{}
			revocations, _ := kg["revocations"].([]any)
			for _, item := range revocations {
				r := object(item)
				stamp, _ := r["at"].(string)
				id, _ := r["alert_id"].(float64)
				if int64(id) != a.ID || localDay(stamp) < a.WindowStart {
					continue
				}
				at = stamp
				list, _ := r["keys"].([]any)
				for _, k := range list {
					if name, ok := k.(string); ok && !seen[name] {
						seen[name] = true
						keys = append(keys, name)
					}
				}
			}
			if at != "" {
				if keys == nil {
					keys = []string{}
				}
				info["revoked"] = map[string]any{"at": at, "keys": keys}
			}
			var match any
			if a.KeyMatch != nil {
				match = *a.KeyMatch
			}
			names, err := matching(kg, match)
			if err != nil {
				names = []string{}
			}
			info["matching"] = names
			info["targets_as_of"] = kg["targets_as_of"]
		}
		a.Keyguard = info
	}
	return alerts
}

// KeyguardSummary is the Alerts page's Keyguard panel, plus an optional preview of a key match.
func KeyguardSummary(config Config, keyMatch *string) (map[string]any, error) {
	kg := KeyguardStatus(config)
	installed, _ := kg["installed"].(bool)
	result := map[string]any{"available": kg["available"], "installed": installed, "error": kg["error"]}
	for k, v := range enforcement(kg) {
		result[k] = v
	}
	if kg["available"] == true {
		for _, k := range []string{"keyguard_version", "bridge_running", "last_failure", "keys", "targets", "targets_as_of", "revocations"} {
			result[k] = kg[k]
		}
		result["cost_center_check"] = kg["cost_center"]
	}
	if keyMatch != nil {
		if kg["available"] == true {
			names, err := matching(kg, *keyMatch)
			if err != nil {
				return nil, err
			}
			result["preview"] = names
		} else {
			// The pattern is still checked, so the form can show its mistakes without Keyguard.
			if _, err := KeyPatterns(*keyMatch); err != nil {
				return nil, err
			}
			result["preview"] = nil
		}
	}
	return result, nil
}

// splitCommand splits a command line on whitespace, honouring single and double quotes and backslashes.
func splitCommand(line string) []string {
	var words []string
	var current strings.Builder
	var quote rune
	inWord, escaped := false, false
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote, inWord = r, true
		case quote == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, current.String())
	}
	return words
}
