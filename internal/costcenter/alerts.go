package costcenter

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Local cost alerts: a USD limit per day, week, or month, for all projects or one.

var periodNames = map[string]string{"day": "daily", "week": "weekly", "month": "monthly"}

const maxWarnings = 4
const maxKeyPatterns = 20

// Limits are whole cents: USD has nothing smaller, and a sub-cent limit would trip on any usage.
var minLimit = mustDec("0.01")

// keyPattern is one Meta Keyguard key-name pattern: letters, digits, '.', '_', '-', '*' and '?'.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._*?-]{1,64}$`)

// ValidationError is a request Cost Center refuses; the message says what to change.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error { return &ValidationError{fmt.Sprintf(format, args...)} }

// window is the local calendar window containing the day: the day, the Monday-start week, or the month.
func window(period string, day time.Time) (time.Time, time.Time) {
	switch period {
	case "week":
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset), day
	case "month":
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location()), day
	}
	return day, day
}

// AlertFields are an alert's stored columns after validation.
type AlertFields struct {
	Period      string
	Project     *string
	BillingMode *string
	LimitUSD    string
	WarnLevels  string
	Enabled     int
	RevokeKeys  int
	KeyMatch    *string
}

// ValidateAlert checks an alert as the dashboard and CLI submit it.
func ValidateAlert(value any) (AlertFields, error) {
	body, ok := value.(map[string]any)
	if !ok {
		return AlertFields{}, invalid("Alert must be a JSON object")
	}
	period, _ := body["period"].(string)
	if periodNames[period] == "" {
		return AlertFields{}, invalid("Period must be day, week, or month")
	}
	var project *string
	if truthy(body["project"]) {
		p, ok := body["project"].(string)
		if !ok || utf8.RuneCountInString(p) > 200 {
			return AlertFields{}, invalid("Project must be a name of at most 200 characters")
		}
		project = &p
	}
	var billingMode *string
	if truthy(body["billing_mode"]) {
		if body["billing_mode"] != "payg" {
			return AlertFields{}, invalid("Billing scope must be all usage or payg")
		}
		payg := "payg"
		billingMode = &payg
	}
	limit, err := ParseDecimal(pyStr(body["limit_usd"]))
	if err != nil {
		return AlertFields{}, invalid("Limit must be a number")
	}
	if !limit.Finite || limit.Value.Cmp(minLimit) < 0 {
		return AlertFields{}, invalid("Limit must be at least $0.01")
	}
	if limit.Exponent < -2 {
		return AlertFields{}, invalid("Limit must be in whole cents, for example 0.50")
	}
	levelsValue, present := body["warn_levels"]
	if !present {
		levelsValue = []any{json.Number("80")}
	}
	list, ok := levelsValue.([]any)
	if !ok || len(list) < 1 || len(list) > maxWarnings {
		return AlertFields{}, invalid("Set between 1 and %d warning levels", maxWarnings)
	}
	levels := make([]int, 0, len(list))
	seen := map[int]bool{}
	for _, item := range list {
		n, ok := item.(json.Number)
		var v int64
		if ok && isIntLiteral(n) {
			v, err = strconv.ParseInt(string(n), 10, 64)
			ok = err == nil
		}
		if !ok || v < 1 || v > 99 {
			return AlertFields{}, invalid("Warning levels must be whole percentages from 1 to 99")
		}
		levels = append(levels, int(v))
	}
	for _, v := range levels {
		if seen[v] {
			return AlertFields{}, invalid("Warning levels must be different from each other")
		}
		seen[v] = true
	}
	sort.Ints(levels)
	revoke := false
	if v, present := body["revoke_keys"]; present {
		revoke = truthy(v)
	}
	var keyMatch *string
	if revoke {
		match, err := KeyPatterns(body["key_match"])
		if err != nil {
			return AlertFields{}, err
		}
		keyMatch = &match
	}
	enabled := 1
	if v, present := body["enabled"]; present && !truthy(v) {
		enabled = 0
	}
	revokeKeys := 0
	if revoke {
		revokeKeys = 1
	}
	return AlertFields{Period: period, Project: project, BillingMode: billingMode, LimitUSD: limit.Text, WarnLevels: pyDumps(levels),
		Enabled: enabled, RevokeKeys: revokeKeys, KeyMatch: keyMatch}, nil
}

// KeyPatterns normalises a key match such as '*', 'bootcamp-*', or 'key1,key2' to comma-separated patterns.
func KeyPatterns(value any) (string, error) {
	var patterns []string
	if text, ok := value.(string); ok {
		for _, p := range strings.Split(text, ",") {
			if p = strings.TrimSpace(p); p != "" {
				patterns = append(patterns, p)
			}
		}
	}
	if len(patterns) == 0 {
		return "", invalid("Key match is required when the alert revokes keys; use * for every key")
	}
	if len(patterns) > maxKeyPatterns {
		return "", invalid("Use at most %d key patterns", maxKeyPatterns)
	}
	var unique []string
	seen := map[string]bool{}
	for _, p := range patterns {
		if !keyPattern.MatchString(p) {
			return "", invalid("Key pattern %s may only use letters, digits, . _ - and the wildcards * and ?", pyRepr(p))
		}
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}
	return strings.Join(unique, ","), nil
}

func (s *Store) CreateAlert(body any) (int64, error) {
	a, err := ValidateAlert(body)
	if err != nil {
		return 0, err
	}
	result, err := s.DB.Exec("INSERT INTO alerts (period, project, billing_mode, limit_usd, warn_levels, enabled, revoke_keys, "+
		"key_match, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.Period, a.Project, a.BillingMode, a.LimitUSD, a.WarnLevels, a.Enabled, a.RevokeKeys, a.KeyMatch, nowISO())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UpdateAlert(id int64, body any) error {
	a, err := ValidateAlert(body)
	if err != nil {
		return err
	}
	result, err := s.DB.Exec("UPDATE alerts SET period=?, project=?, billing_mode=?, limit_usd=?, warn_levels=?, enabled=?, "+
		"revoke_keys=?, key_match=? WHERE id=?",
		a.Period, a.Project, a.BillingMode, a.LimitUSD, a.WarnLevels, a.Enabled, a.RevokeKeys, a.KeyMatch, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return &NotFound{fmt.Sprintf("Unknown alert: %d", id)}
	}
	return nil
}

func (s *Store) DeleteAlert(id int64) error {
	result, err := s.DB.Exec("DELETE FROM alerts WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return &NotFound{fmt.Sprintf("Unknown alert: %d", id)}
	}
	return nil
}

func describe(period string, project, billingMode *string) string {
	scope := "all projects"
	if project != nil && *project != "" {
		scope = *project
	}
	mode := ""
	if billingMode != nil && *billingMode == "payg" {
		mode = ", pay as you go only"
	}
	name := periodNames[period]
	if name == "" {
		name = period
	}
	return strings.ToUpper(name[:1]) + name[1:] + " limit for " + scope + mode
}

// AlertStatus is an alert with its current spend. Meta Keyguard reads these from
// `meta-cost alert status --json` to decide which keys to revoke.
type AlertStatus struct {
	ID             int64          `json:"id"`
	Period         string         `json:"period"`
	Project        *string        `json:"project"`
	BillingMode    *string        `json:"billing_mode"`
	LimitUSD       string         `json:"limit_usd"`
	WarnLevels     []int          `json:"warn_levels"`
	Enabled        bool           `json:"enabled"`
	CreatedAt      *string        `json:"created_at"`
	RevokeKeys     bool           `json:"revoke_keys"`
	KeyMatch       *string        `json:"key_match"`
	WarningAt      *int           `json:"warning_at"`
	Label          string         `json:"label"`
	WindowStart    string         `json:"window_start"`
	WindowEnd      string         `json:"window_end"`
	SpentUSD       Dec            `json:"spent_usd"`
	Percent        pyFloatJSON    `json:"percent"`
	Level          string         `json:"level"`
	UnpricedEvents int            `json:"unpriced_events"`
	AssumedEvents  int            `json:"assumed_events"`
	Keyguard       map[string]any `json:"keyguard,omitempty"`
}

// AlertStatuses is the current spend against every alert. Unpriced usage is excluded, so spend is a lower bound.
func (s *Store) AlertStatuses(rates []Rate, day time.Time) ([]AlertStatus, error) {
	rows, err := s.DB.Query("SELECT id, period, project, billing_mode, limit_usd, warn_levels, enabled, created_at, revoke_keys, " +
		"key_match FROM alerts ORDER BY id")
	if err != nil {
		return nil, err
	}
	result := []AlertStatus{}
	for rows.Next() {
		var a AlertStatus
		var period, project, billing, limit, levels, created, keyMatch sql.NullString
		var enabled, revoke sql.NullInt64
		if err := rows.Scan(&a.ID, &period, &project, &billing, &limit, &levels, &enabled, &created, &revoke, &keyMatch); err != nil {
			rows.Close()
			return nil, err
		}
		a.Period, a.LimitUSD = period.String, limit.String
		if project.Valid {
			a.Project = &project.String
		}
		if billing.Valid {
			a.BillingMode = &billing.String
		}
		if created.Valid {
			a.CreatedAt = &created.String
		}
		if keyMatch.Valid {
			a.KeyMatch = &keyMatch.String
		}
		var parsed []float64
		json.Unmarshal([]byte(levels.String), &parsed)
		a.WarnLevels = []int{}
		for _, v := range parsed {
			a.WarnLevels = append(a.WarnLevels, int(v))
		}
		a.Enabled, a.RevokeKeys = enabled.Int64 != 0, revoke.Int64 != 0
		result = append(result, a)
	}
	rows.Close()
	if len(result) == 0 {
		return result, nil
	}
	earliest := day
	for i := range result {
		start, end := window(result[i].Period, day)
		result[i].WindowStart, result[i].WindowEnd = dateString(start), dateString(end)
		if start.Before(earliest) {
			earliest = start
		}
	}
	usage, err := s.Query(Filters{Period: "all", From: dateString(earliest), To: dateString(day)})
	if err != nil {
		return nil, err
	}
	for i := range result {
		a := &result[i]
		var items []Row
		for _, r := range usage {
			if localDay(r.RecordedAt) >= a.WindowStart && (a.Project == nil || *a.Project == "" || r.Project == *a.Project) &&
				(a.BillingMode == nil || *a.BillingMode == "" || r.BillingMode == *a.BillingMode) {
				items = append(items, r)
			}
		}
		t := totals(items, rates)
		a.SpentUSD = t.Equivalent()
		limit := mustDec(a.LimitUSD)
		// percent is exact for comparisons and rounded to one place for display.
		var percent Dec
		if limit.Sign() > 0 {
			percent = Dec{r: new(big.Rat).Quo(a.SpentUSD.MulInt(100).rat(), limit.rat())}
		}
		rounded, _ := strconv.ParseFloat(strconv.FormatFloat(percent.Float64(), 'f', 1, 64), 64)
		a.Percent = pyFloatJSON(rounded)
		// The highest warning level reached; each one notifies separately.
		for _, v := range a.WarnLevels {
			if percent.Cmp(decInt(int64(v))) >= 0 && (a.WarningAt == nil || v > *a.WarningAt) {
				level := v
				a.WarningAt = &level
			}
		}
		switch {
		case !a.Enabled:
			a.Level = "paused"
		case percent.Cmp(decInt(100)) >= 0:
			a.Level = "exceeded"
		case a.WarningAt != nil:
			a.Level = "warning"
		default:
			a.Level = "ok"
		}
		a.Label = describe(a.Period, a.Project, a.BillingMode)
		a.UnpricedEvents, a.AssumedEvents = t.CostUnknownEvents, t.CostAssumedEvents
	}
	return result, nil
}
