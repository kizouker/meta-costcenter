package costcenter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// event is a usage log record like Muse writes, with prompt and response text that must never be stored.
func event(id, session string, input int64, cached any, model string) map[string]any {
	return map[string]any{"id": id, "stream": map[string]any{"id": session}, "sequence": 1,
		"recorded_at": time.Now().UnixMicro(),
		"payload": map[string]any{"event": map[string]any{"kind": "model_completed", "model": model,
			"usage":    map[string]any{"input_tokens": input, "output_tokens": 10, "cached_tokens": cached},
			"response": "SECRET_RESPONSE"}, "prompt": "SECRET_PROMPT"}}
}

func defaultEvent() map[string]any { return event("event-1", "parent", 100, 20, "test-model") }

type fixture struct {
	t     *testing.T
	root  string
	logs  string
	path  string
	store *Store
	rates []Rate
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, logs: filepath.Join(root, "logs")}
	f.path = filepath.Join(f.logs, "parent", "session.jsonl")
	f.open()
	t.Cleanup(func() { f.store.Close() })
	f.rates = []Rate{{Model: "test-model", EffectiveFrom: "2020-01-01", SnapshotVersion: "fixture",
		Input: mustDec("1"), CachedInput: mustDec("0.1"), Output: mustDec("2")}}
	return f
}

func (f *fixture) open() {
	store, err := OpenStore(filepath.Join(f.root, "db.sqlite3"))
	if err != nil {
		f.t.Fatal(err)
	}
	f.store = store
}

func (f *fixture) reopen() {
	f.store.Close()
	f.open()
}

// write appends a record to a log, or to the default session log when path is empty.
func (f *fixture) write(record any, path string) {
	f.t.Helper()
	if path == "" {
		path = f.path
	}
	data, err := json.Marshal(record)
	if err != nil {
		f.t.Fatal(err)
	}
	f.appendText(path, string(data)+"\n")
}

func (f *fixture) appendText(path, text string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan(sessions map[string]SessionMeta) ScanResult {
	f.t.Helper()
	result, err := f.store.Scan([]string{f.logs}, sessions)
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *fixture) query(filters Filters) []Row {
	f.t.Helper()
	rows, err := f.store.Query(filters)
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func (f *fixture) rows() []Row { return f.query(Filters{Period: "all"}) }

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.store.DB.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) intValue(query string) int64 {
	f.t.Helper()
	var n int64
	if err := f.store.DB.QueryRow(query).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// dump is every stored text value, for checking that nothing private was kept.
func (f *fixture) dump() string {
	f.t.Helper()
	tables, err := f.store.DB.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		f.t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		tables.Scan(&name)
		names = append(names, name)
	}
	tables.Close()
	var b strings.Builder
	for _, name := range names {
		rows, err := f.store.DB.Query("SELECT * FROM " + name)
		if err != nil {
			f.t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			rows.Scan(pointers...)
			for _, v := range values {
				switch v := v.(type) {
				case string:
					b.WriteString(v + "\n")
				case []byte:
					b.WriteString(string(v) + "\n")
				}
			}
		}
		rows.Close()
	}
	return b.String()
}

// rawAlert inserts an alert without validation, for limits smaller than the fixture's sub-cent spend.
func (f *fixture) rawAlert(columns map[string]any) int64 {
	f.t.Helper()
	fields := map[string]any{"warn_levels": "[80]", "enabled": 1}
	for k, v := range columns {
		fields[k] = v
	}
	var names, marks []string
	var values []any
	for k, v := range fields {
		names, marks, values = append(names, k), append(marks, "?"), append(values, v)
	}
	result, err := f.store.DB.Exec("INSERT INTO alerts ("+strings.Join(names, ", ")+") VALUES ("+strings.Join(marks, ", ")+")", values...)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}

func (f *fixture) alerts(rates []Rate) []AlertStatus {
	f.t.Helper()
	statuses, err := f.store.AlertStatuses(rates, today())
	if err != nil {
		f.t.Fatal(err)
	}
	return statuses
}

// jsonBody decodes JSON as the dashboard decodes request bodies.
func jsonBody(t *testing.T, text string) any {
	t.Helper()
	value, err := decodeJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func decEqual(t *testing.T, got Dec, want string) {
	t.Helper()
	if got.Cmp(mustDec(want)) != 0 {
		t.Errorf("got %s, want %s", got, want)
	}
}

func str(s string) *string { return &s }

func isNotFound(err error) bool {
	var notFound *NotFound
	return errors.As(err, &notFound)
}

func isInvalid(err error) bool {
	var validation *ValidationError
	return errors.As(err, &validation)
}

func TestRescanPartialAndPrivacy(t *testing.T) {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	if got := f.scan(nil).Added; got != 1 {
		t.Fatalf("first scan added %d", got)
	}
	if got := f.scan(nil).Added; got != 0 {
		t.Fatalf("rescan added %d", got)
	}
	record, _ := json.Marshal(event("event-2", "parent", 100, 20, "test-model"))
	f.appendText(f.path, string(record[:50]))
	if got := f.scan(nil).Added; got != 0 {
		t.Fatalf("partial line added %d", got)
	}
	if got := f.intValue("SELECT pending FROM sources"); got != 1 {
		t.Fatalf("pending = %d", got)
	}
	f.appendText(f.path, string(record[50:])+"\n")
	if got := f.scan(nil).Added; got != 1 {
		t.Fatalf("completed line added %d", got)
	}
	if strings.Contains(f.dump(), "SECRET_") {
		t.Fatal("prompt or response text was stored")
	}
	if got := len(f.rows()); got != 2 {
		t.Fatalf("%d rows", got)
	}
}

func TestSubagentAndSummaryNotAdded(t *testing.T) {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	f.write(event("child-event", "child", 50, 20, "test-model"), filepath.Join(f.logs, "parent/subagent/child/session.jsonl"))
	f.write(map[string]any{"id": "summary", "type": "run.usage", "session_id": "parent",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"usage": map[string]any{"models": []any{map[string]any{"model": "test-model", "input_tokens": 150,
			"output_tokens": 20, "cached_tokens": 40}}}}, "")
	f.scan(nil)
	rows := f.query(Filters{Period: "all", Session: "parent"})
	var input int64
	for _, r := range rows {
		input += r.Input
	}
	if input != 150 || len(rows) != 2 {
		t.Fatalf("input %d over %d rows", input, len(rows))
	}
	reconciled, err := f.store.Reconciliation()
	if err != nil {
		t.Fatal(err)
	}
	if reconciled[0].Status != "match" {
		t.Fatalf("reconciliation %+v", reconciled)
	}
}

func TestReplacementAndCopyDedup(t *testing.T) {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	f.scan(nil)
	f.write(defaultEvent(), filepath.Join(f.logs, "copied/session.jsonl"))
	os.Remove(f.path)
	f.write(event("event-2", "parent", 100, 20, "test-model"), "")
	f.scan(nil)
	if got := len(f.rows()); got != 2 {
		t.Fatalf("%d rows", got)
	}
	if f.intValue("SELECT COUNT(*) FROM sources WHERE status LIKE '%retained%'") == 0 {
		t.Fatal("replaced source not marked retained")
	}
}

func TestInvalidAndUnknown(t *testing.T) {
	f := newFixture(t)
	f.write(event("event-1", "parent", -2, 20, "test-model"), "")
	f.write(event("unknown", "parent", 100, nil, "other"), "")
	f.scan(nil)
	if got := len(f.rows()); got != 1 {
		t.Fatalf("%d rows", got)
	}
	if got := totals(f.rows(), f.rates).CostUnknownEvents; got != 1 {
		t.Fatalf("cost unknown %d", got)
	}
	if got := f.intValue("SELECT errors FROM sources"); got != 1 {
		t.Fatalf("errors %d", got)
	}
	f.scan(nil)
	if got := f.intValue("SELECT errors FROM sources"); got != 1 {
		t.Fatalf("errors after rescan %d", got)
	}
}

func TestEstimateAndFilters(t *testing.T) {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	f.scan(map[string]SessionMeta{"parent": {Project: str("demo"), BillingMode: str("subscription")}})
	got := totals(f.query(Filters{Period: "all", Project: "demo", BillingMode: "subscription"}), f.rates)
	decEqual(t, got.SubscriptionEquivalentUSD, "0.000102")
	decEqual(t, got.PaygUSD, "0")
	if rows := f.query(Filters{Period: "all", Model: "missing"}); len(rows) != 0 {
		t.Fatalf("%d rows for a missing model", len(rows))
	}
	text, err := f.store.Lines(Filters{Period: "all"}, f.rates, "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(text, "\n"), "Not entered") {
		t.Fatal("no manual observation note")
	}
	text, err = f.store.Lines(Filters{Period: "all", From: "2000-01-01", To: "2000-01-02"}, f.rates, "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	// No usage costs nothing; it is not unknown.
	if !strings.Contains(strings.Join(text, "\n"), "Estimated PAYG: USD 0.000000") {
		t.Fatal(strings.Join(text, "\n"))
	}
}

func TestNativeMetadataSurvivesIncrementalScan(t *testing.T) {
	f := newFixture(t)
	f.write(map[string]any{"payload_type": "runtime.session.metadata", "payload": map[string]any{"record": map[string]any{
		"workspace_root": "/private/work/demo", "model_id": "test-model"}}}, "")
	record := defaultEvent()
	delete(record["payload"].(map[string]any)["event"].(map[string]any), "model")
	f.write(record, "")
	f.scan(nil)
	if r := f.rows()[0]; r.Project != "demo" || r.Model != "test-model" {
		t.Fatalf("row %+v", r)
	}
	record["id"] = "next"
	f.write(record, "")
	f.scan(map[string]SessionMeta{"parent": {BillingMode: str("payg")}})
	for _, r := range f.rows() {
		if r.Project != "demo" || r.BillingMode != "payg" {
			t.Fatalf("row %+v", r)
		}
	}
	if strings.Contains(f.dump(), "/private/work") {
		t.Fatal("workspace path was stored")
	}
}

func TestUnavailableIsNotZero(t *testing.T) {
	f := newFixture(t)
	result, err := f.store.Scan([]string{filepath.Join(f.root, "missing")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("no warning for a missing source")
	}
}

func TestUnsupportedSummaryIsDiagnosed(t *testing.T) {
	f := newFixture(t)
	f.write(map[string]any{"type": "run.usage", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"usage": map[string]any{"new_shape": true}}, "")
	f.scan(nil)
	if got := f.intValue("SELECT errors FROM sources"); got != 1 {
		t.Fatalf("errors %d", got)
	}
}

func drillDownFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.write(defaultEvent(), "")
	f.write(event("child-event", "child", 50, 20, "test-model"), filepath.Join(f.logs, "parent/subagent/child/session.jsonl"))
	f.write(event("grand-event", "grand", 30, 20, "test-model"), filepath.Join(f.logs, "parent/subagent/child/subagent/grand/session.jsonl"))
	f.write(event("orphan-event", "orphan", 7, nil, "test-model"), filepath.Join(f.logs, "gone/subagent/orphan/session.jsonl"))
	f.write(event("other-event", "other", 5, 0, "test-model"), filepath.Join(f.logs, "other/session.jsonl"))
	f.scan(nil)
	return f
}

func TestSessionsRollUpIntoRoots(t *testing.T) {
	f := drillDownFixture(t)
	listed, err := f.store.SessionsFor(Filters{Period: "all"}, f.rates)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]SessionSummary{}
	var input int64
	for _, s := range listed {
		rows[s.ID] = s
		input += s.Input
	}
	// The orphan's parent has no usage, so it is listed as its own root.
	if len(rows) != 3 || rows["parent"].ID == "" || rows["orphan"].ID == "" || rows["other"].ID == "" {
		t.Fatalf("roots %v", rows)
	}
	parent := rows["parent"]
	if parent.Input != 180 || parent.Subagents != 2 || strings.Join(parent.Models, ",") != "test-model" {
		t.Fatalf("parent %+v", parent)
	}
	if rows["orphan"].CostUnknownEvents != 1 {
		t.Fatalf("orphan %+v", rows["orphan"])
	}
	empty, err := f.store.SessionsFor(Filters{Period: "all", From: "2000-01-01", To: "2000-01-02"}, f.rates)
	if err != nil || len(empty) != 0 {
		t.Fatalf("%v %v", empty, err)
	}
	if want := totals(f.rows(), f.rates).Input; input != want {
		t.Fatalf("sessions sum to %d, totals %d", input, want)
	}
}

func TestSessionDetailTree(t *testing.T) {
	f := drillDownFixture(t)
	detail, err := f.store.SessionDetail("parent", Filters{Period: "all", Project: "ignored"}, f.rates)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Totals.Input != 180 || detail.Own.Input != 100 {
		t.Fatalf("totals %d own %d", detail.Totals.Input, detail.Own.Input)
	}
	tree := detail.Tree
	if ids := []string{tree.ID, tree.Children[0].ID, tree.Children[0].Children[0].ID}; strings.Join(ids, ",") != "parent,child,grand" {
		t.Fatalf("tree %v", ids)
	}
	var nodeSum func(TreeNode) int64
	nodeSum = func(n TreeNode) int64 {
		sum := n.Input
		for _, c := range n.Children {
			sum += nodeSum(c)
		}
		return sum
	}
	if nodeSum(*tree) != detail.Totals.Input {
		t.Fatal("tree does not add up to the totals")
	}
	if len(detail.Events) != 3 || detail.TimelineUnit != "hour" {
		t.Fatalf("%d events, unit %s", len(detail.Events), detail.TimelineUnit)
	}
	data, _ := json.Marshal(detail.Events[0])
	if strings.Contains(string(data), `"source"`) {
		t.Fatal("events expose their source path")
	}
	child, err := f.store.SessionDetail("child", Filters{Period: "all"}, f.rates)
	if err != nil {
		t.Fatal(err)
	}
	if *child.Parent != "parent" || child.Root != "parent" || child.Totals.Input != 80 {
		t.Fatalf("child %+v", child)
	}
	if _, err := f.store.SessionDetail("missing", Filters{Period: "all"}, f.rates); !isNotFound(err) {
		t.Fatalf("missing session: %v", err)
	}
}

func TestSubagentsInheritRootProject(t *testing.T) {
	f := drillDownFixture(t)
	f.exec("UPDATE events SET project='site' WHERE session='parent'")
	sessions := map[string]bool{}
	for _, r := range f.query(Filters{Period: "all", Project: "site"}) {
		sessions[r.Session] = true
	}
	if len(sessions) != 3 || !sessions["parent"] || !sessions["child"] || !sessions["grand"] {
		t.Fatalf("sessions %v", sessions)
	}
	detail, err := f.store.ProjectDetail("site", Filters{Period: "all"}, f.rates)
	if err != nil || detail.Totals.Input != 180 {
		t.Fatalf("%+v %v", detail, err)
	}
	if got := f.query(Filters{Period: "all", Session: "orphan"})[0].Project; got != "unassigned" {
		t.Fatalf("orphan project %q", got)
	}
}

func TestProjectDetailMatchesBreakdown(t *testing.T) {
	f := drillDownFixture(t)
	listed, err := f.store.Projects(f.rows(), f.rates)
	if err != nil {
		t.Fatal(err)
	}
	var unassigned ProjectSummary
	for _, p := range listed {
		if p.Label == "unassigned" {
			unassigned = p
		}
	}
	if unassigned.Sessions != 3 {
		t.Fatalf("unassigned %+v", unassigned)
	}
	detail, err := f.store.ProjectDetail("unassigned", Filters{Period: "all", Model: "ignored"}, f.rates)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Totals.Input != unassigned.Input || len(detail.Sessions) != 3 {
		t.Fatalf("detail %+v", detail)
	}
	if _, err := f.store.ProjectDetail("missing", Filters{Period: "all"}, f.rates); !isNotFound(err) {
		t.Fatalf("missing project: %v", err)
	}
}

func pricingFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.write(defaultEvent(), "") // test-model: priced
	f.write(event("private-event", "private", 100, 20, "secret-model"), filepath.Join(f.logs, "private/session.jsonl"))
	f.scan(nil)
	return f
}

func (f *fixture) ratesFrom(day string) []Rate {
	rate := f.rates[0]
	rate.EffectiveFrom = day
	return []Rate{rate}
}

func TestBackPricingIsAssumed(t *testing.T) {
	f := pricingFixture(t)
	row := f.query(Filters{Period: "all", Model: "test-model"})[0]
	value, _, assumed, _ := estimate(row, f.ratesFrom("2999-01-01"))
	decEqual(t, value, "0.000102")
	if !assumed {
		t.Fatal("a rate from after the event was not marked assumed")
	}
	if _, _, assumed, _ := estimate(row, f.rates); assumed {
		t.Fatal("a current rate was marked assumed")
	}
	got := totals(f.rows(), f.ratesFrom("2999-01-01"))
	if got.CostAssumedEvents != 1 || got.CostUnknownEvents != 1 || got.PricedTokens != 110 {
		t.Fatalf("totals %+v", got)
	}
}

func TestLocalRateOverridesAndPricesUnknownModels(t *testing.T) {
	f := pricingFixture(t)
	if err := f.store.SetLocalRate("secret-model", jsonBody(t, `{"input_per_million":"2","cached_input_per_million":"1","output_per_million":"4"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetLocalRate("test-model", jsonBody(t, `{"input_per_million":"0","cached_input_per_million":"0","output_per_million":"0"}`)); err != nil {
		t.Fatal(err)
	}
	rates, err := f.store.WithLocalRates(f.rates)
	if err != nil {
		t.Fatal(err)
	}
	got := totals(f.rows(), rates)
	if got.CostUnknownEvents != 0 {
		t.Fatalf("cost unknown %d", got.CostUnknownEvents)
	}
	decEqual(t, got.UnknownModeEquivalentUSD, "0.00022")
	source := func(rates []Rate) map[string]string {
		table, err := f.store.PricingTable(rates)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]string{}
		for _, m := range table {
			result[m.Model] = m.Source
		}
		return result
	}
	if s := source(rates); s["secret-model"] != "local" || s["test-model"] != "local" {
		t.Fatalf("sources %v", s)
	}
	// Merging twice does not duplicate the local rates.
	twice, _ := f.store.WithLocalRates(rates)
	n := 0
	for _, r := range twice {
		if r.Model == "secret-model" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d secret-model rates after merging twice", n)
	}
	if err := f.store.DeleteLocalRate("secret-model"); err != nil {
		t.Fatal(err)
	}
	rates, _ = f.store.WithLocalRates(f.rates)
	if s := source(rates); s["secret-model"] != "missing" {
		t.Fatalf("sources %v", s)
	}
	if err := f.store.SetLocalRate("x", jsonBody(t, `{"input_per_million":"-1"}`)); !isInvalid(err) {
		t.Fatalf("negative rate: %v", err)
	}
	if err := f.store.DeleteLocalRate("secret-model"); !isNotFound(err) {
		t.Fatalf("deleting twice: %v", err)
	}
}

func TestAlertWindowsScopesAndLevels(t *testing.T) {
	f := pricingFixture(t)
	start, end := window("week", time.Date(2026, 10, 24, 0, 0, 0, 0, time.Local))
	if dateString(start) != "2026-10-19" || dateString(end) != "2026-10-24" {
		t.Fatalf("week %s..%s", dateString(start), dateString(end))
	}
	if start, _ := window("month", time.Date(2026, 10, 24, 0, 0, 0, 0, time.Local)); dateString(start) != "2026-10-01" {
		t.Fatalf("month starts %s", dateString(start))
	}
	f.exec("UPDATE events SET project='site'")
	// The fixture spends far less than a cent, so these limits are stored directly, past validation.
	f.rawAlert(map[string]any{"period": "day", "limit_usd": "0.0001"})                                                     // spent 0.000102: exceeded
	f.rawAlert(map[string]any{"period": "week", "project": "site", "limit_usd": "0.00012", "warn_levels": "[50, 80, 90]"}) // 85%: warning at 80
	if _, err := f.store.CreateAlert(jsonBody(t, `{"period":"month","project":"other","limit_usd":"1"}`)); err != nil {    // nothing spent
		t.Fatal(err)
	}
	if _, err := f.store.CreateAlert(jsonBody(t, `{"period":"month","billing_mode":"payg","limit_usd":"1","enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	statuses := f.alerts(f.rates)
	var levels []string
	for _, a := range statuses {
		levels = append(levels, a.Level)
	}
	if strings.Join(levels, ",") != "exceeded,warning,ok,paused" {
		t.Fatalf("levels %v", levels)
	}
	if fmtLevels, _ := json.Marshal(statuses[1].WarnLevels); string(fmtLevels) != "[50,80,90]" || *statuses[1].WarningAt != 80 {
		t.Fatalf("warning %+v", statuses[1])
	}
	decEqual(t, statuses[0].SpentUSD, "0.000102")
	if statuses[0].UnpricedEvents != 1 {
		t.Fatalf("unpriced %d", statuses[0].UnpricedEvents)
	}
	for _, bad := range []string{`{"period":"year","limit_usd":"1"}`, `{"period":"day","limit_usd":"0"}`, `{"period":"day","limit_usd":"x"}`,
		`{"period":"day","limit_usd":"1","warn_levels":[100]}`, `{"period":"day","limit_usd":"1","warn_levels":[]}`,
		`{"period":"day","limit_usd":"1","warn_levels":[10,20,30,40,50]}`, `{"period":"day","limit_usd":"1","warn_levels":[50,50]}`,
		`{"period":"day","limit_usd":"1","billing_mode":"subscription"}`,
		`{"period":"day","limit_usd":"0.009"}`, `{"period":"day","limit_usd":"0.015"}`} {
		if _, err := ValidateAlert(jsonBody(t, bad)); !isInvalid(err) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if err := f.store.DeleteAlert(1); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateAlert(1, jsonBody(t, `{"period":"day","limit_usd":"1"}`)); !isNotFound(err) {
		t.Fatalf("updating a deleted alert: %v", err)
	}
}

func TestAlertKeyRevocationSettings(t *testing.T) {
	f := pricingFixture(t)
	valid, err := ValidateAlert(jsonBody(t, `{"period":"day","limit_usd":"1","revoke_keys":true,"key_match":" bootcamp-* , key1,key1 "}`))
	if err != nil || valid.RevokeKeys != 1 || *valid.KeyMatch != "bootcamp-*,key1" {
		t.Fatalf("%+v %v", valid, err)
	}
	// The pattern is dropped when revocation is off, so a stale pattern never revokes anything.
	if valid, _ := ValidateAlert(jsonBody(t, `{"period":"day","limit_usd":"1","key_match":"*"}`)); valid.KeyMatch != nil {
		t.Fatalf("key match kept: %q", *valid.KeyMatch)
	}
	for _, bad := range []string{"", " , ", "has space", "semi;colon", strings.Repeat("k,", 20) + "k"} {
		request, _ := json.Marshal(map[string]any{"period": "day", "limit_usd": "1", "revoke_keys": true, "key_match": bad})
		if _, err := ValidateAlert(jsonBody(t, string(request))); !isInvalid(err) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if valid, _ := ValidateAlert(jsonBody(t, `{"period":"day","limit_usd":"0.01"}`)); valid.LimitUSD != "0.01" {
		t.Fatalf("limit %q", valid.LimitUSD)
	}
	id := f.rawAlert(map[string]any{"period": "day", "limit_usd": "0.0001", "revoke_keys": 1, "key_match": "*"})
	find := func() AlertStatus {
		for _, a := range f.alerts(f.rates) {
			if a.ID == id {
				return a
			}
		}
		t.Fatal("alert not listed")
		return AlertStatus{}
	}
	if a := find(); !a.RevokeKeys || *a.KeyMatch != "*" || a.Level != "exceeded" {
		t.Fatalf("%+v", a)
	}
	if err := f.store.UpdateAlert(id, jsonBody(t, `{"period":"day","limit_usd":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if a := find(); a.RevokeKeys || a.KeyMatch != nil {
		t.Fatalf("%+v", a)
	}
}

func TestKeyRevocationColumnsMigrate(t *testing.T) {
	f := pricingFixture(t)
	f.exec(`DROP TABLE alerts`)
	f.exec(`CREATE TABLE alerts (id INTEGER PRIMARY KEY, period TEXT, project TEXT, billing_mode TEXT,
		limit_usd TEXT, warn_levels TEXT DEFAULT '[80]', enabled INTEGER DEFAULT 1, created_at TEXT)`)
	f.exec(`INSERT INTO alerts (period, limit_usd) VALUES ('day', '5')`)
	f.reopen()
	if f.alerts(f.rates)[0].RevokeKeys {
		t.Fatal("a migrated alert revokes keys")
	}
}

func TestSingleWarningColumnMigratesToLevels(t *testing.T) {
	f := pricingFixture(t)
	f.exec(`DROP TABLE alerts`)
	f.exec(`CREATE TABLE alerts (id INTEGER PRIMARY KEY, period TEXT, project TEXT, billing_mode TEXT,
		limit_usd TEXT, warn_percent INTEGER DEFAULT 80, enabled INTEGER DEFAULT 1, created_at TEXT)`)
	f.exec(`INSERT INTO alerts (period, limit_usd, warn_percent) VALUES ('day', '5', 70)`)
	f.reopen()
	if levels := f.alerts(f.rates)[0].WarnLevels; len(levels) != 1 || levels[0] != 70 {
		t.Fatalf("levels %v", levels)
	}
	columns, err := f.store.columns("alerts")
	if err != nil {
		t.Fatal(err)
	}
	if columns["warn_percent"] {
		t.Fatal("warn_percent column kept")
	}
}

func TestMonthlyWarningMigratesToAlert(t *testing.T) {
	f := pricingFixture(t)
	f.exec(`INSERT INTO settings VALUES ('monthly_usd','30')`)
	f.exec(`INSERT INTO settings VALUES ('alert_levels','[50, 80, 100]')`)
	f.reopen()
	var period, limit, levels string
	var project *string
	if err := f.store.DB.QueryRow("SELECT period, project, limit_usd, warn_levels FROM alerts").Scan(&period, &project, &limit, &levels); err != nil {
		t.Fatal(err)
	}
	if period != "month" || project != nil || limit != "30" || levels != "[50, 80]" {
		t.Fatalf("%s %v %s %s", period, project, limit, levels)
	}
	if f.intValue("SELECT COUNT(*) FROM settings WHERE key='monthly_usd'") != 0 {
		t.Fatal("monthly_usd setting kept")
	}
}

func TestDecimalTextMatchesPython(t *testing.T) {
	for _, c := range []struct {
		value func() Dec
		want  string
	}{
		{func() Dec { return mustDec("0.0").MulInt(0) }, "0.0"},
		{func() Dec { return mustDec("1").MulInt(102).DivInt(1_000_000) }, "0.000102"},
		{func() Dec { return mustDec("2").MulInt(1).DivInt(1_000_000_000) }, "2E-9"},
		{func() Dec { return mustDec("30") }, "30"},
		{func() Dec { return mustDec("1.50").Add(mustDec("2")) }, "3.50"},
	} {
		if got := c.value().String(); got != c.want {
			t.Errorf("got %s, want %s", got, c.want)
		}
	}
	if got := mustDec("1234.5").Fixed(2, true); got != "1,234.50" {
		t.Errorf("Fixed: %s", got)
	}
	if got := mustDec("0.0000005").Fixed(6, false); got != "0.000000" {
		t.Errorf("Fixed rounds half to even: %s", got)
	}
}
