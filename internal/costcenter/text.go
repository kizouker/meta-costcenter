package costcenter

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// The text report printed by the CLI and shown in the terminal dashboard.

func money(value Dec, unknown int) string {
	note := " (token-only estimate)"
	if unknown > 0 {
		note = " (known subtotal; incomplete)"
	}
	return "USD " + value.Fixed(6, true) + note
}

// activeFilters lists the filters that are set, in a fixed order, for the report header.
func activeFilters(f Filters) string {
	var parts []string
	for _, pair := range [][2]string{{"period", f.Period}, {"from", f.From}, {"to", f.To}, {"project", f.Project},
		{"model", f.Model}, {"session", f.Session}, {"billing_mode", f.BillingMode}, {"sort", f.Sort}} {
		if pair[1] != "" {
			parts = append(parts, pair[0]+"="+pair[1])
		}
	}
	if len(parts) == 0 {
		return "today"
	}
	return strings.Join(parts, ", ")
}

// Lines is the text report for the filters, grouped by model, project, session, or billing_mode.
func (s *Store) Lines(f Filters, rates []Rate, group string, scan *ScanResult) ([]string, error) {
	rows, err := s.Query(f)
	if err != nil {
		return nil, err
	}
	t := totals(rows, rates)
	var lastScan sql.NullString
	var errorCount, pending sql.NullInt64
	if err := s.DB.QueryRow("SELECT MAX(scanned_at), SUM(errors), SUM(pending) FROM sources").Scan(&lastScan, &errorCount, &pending); err != nil {
		return nil, err
	}
	monthFilters := f
	monthFilters.Period, monthFilters.From, monthFilters.To = "month", "", ""
	monthRows, err := s.Query(monthFilters)
	if err != nil {
		return nil, err
	}
	month := totals(monthRows, rates)
	scanned := "never"
	if lastScan.Valid {
		scanned = lastScan.String
	}
	priced := func(value Dec) string {
		if t.Events == 0 || t.CostUnknownEvents < t.Events {
			return money(value, t.CostUnknownEvents)
		}
		return "unknown / no priced observations"
	}
	text := []string{
		"MUSE COST | observed local usage",
		"Filters: " + activeFilters(f),
		fmt.Sprintf("Last scan: %s | malformed/unsupported: %d | partial lines: %d", scanned, errorCount.Int64, pending.Int64),
		fmt.Sprintf("Month-to-date (same non-date filters): input %s | output %s", commas(month.Input), commas(month.Output)),
		fmt.Sprintf("Events %s | Input %s | Output %s | Cached %s", commas(int64(t.Events)), commas(t.Input), commas(t.Output), commas(t.Cached)),
		"Estimated PAYG: " + priced(t.PaygUSD),
		"Subscription API-equivalent: " + priced(t.SubscriptionEquivalentUSD),
		"Unknown billing mode API-equivalent: " + priced(t.UnknownModeEquivalentUSD),
		fmt.Sprintf("Cost unknown: %d events | Cache unknown: %d events", t.CostUnknownEvents, t.CacheUnknownEvents),
		"Coverage: local retained logs only; non-token charges unknown; estimates are not bills.",
	}
	if len(rows) == 0 {
		text = append(text, "No events match filters. Collection availability is shown separately above/below.")
	}
	if scan != nil {
		for _, w := range scan.Warnings {
			text = append(text, "WARNING: "+w)
		}
	}
	text = append(text, "", padRight(strings.ToUpper(group), 36)+" "+padLeft("INPUT", 12)+" "+padLeft("OUTPUT", 12)+" "+
		padLeft("API EQ USD*", 14)+" "+padLeft("EVENTS", 8))
	labels, groups := bucket(rows, func(r Row) string { return r.field(group) })
	if f.Sort == "estimated-cost" {
		sort.SliceStable(labels, func(i, j int) bool {
			return totals(groups[labels[i]], rates).Equivalent().Cmp(totals(groups[labels[j]], rates).Equivalent()) > 0
		})
	} else {
		sort.Strings(labels)
	}
	for _, label := range labels {
		items := groups[label]
		v := totals(items, rates)
		display := "unknown"
		if v.CostUnknownEvents != len(items) {
			display = v.Equivalent().Fixed(6, false)
			if v.CostUnknownEvents > 0 {
				display += "+"
			}
		}
		text = append(text, padRight(truncate(label, 36), 36)+" "+padLeft(commas(v.Input), 12)+" "+padLeft(commas(v.Output), 12)+" "+
			padLeft(display, 14)+" "+padLeft(commas(int64(len(items))), 8))
	}
	text = append(text, "* API-equivalent token estimate, not a bill; + means incomplete subtotal.")
	if f.Session != "" {
		text = append(text, "", "PROVENANCE (last 30 events; session filter includes descendants)")
		tail := rows
		if len(tail) > 30 {
			tail = tail[len(tail)-30:]
		}
		for _, r := range tail {
			parent := "none"
			if r.Parent != nil && *r.Parent != "" {
				parent = *r.Parent
			}
			text = append(text, fmt.Sprintf("%s %s parent=%s %s in=%d out=%d record=%s id=%s",
				r.RecordedAt, r.Session, parent, r.Model, r.Input, r.Output, r.Sequence, r.ID))
		}
	}
	observations, err := s.Observations(3)
	if err != nil {
		return nil, err
	}
	var summaries int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM summaries").Scan(&summaries); err != nil {
		return nil, err
	}
	if summaries > 0 {
		text = append(text, fmt.Sprintf("Run summaries: %d comparison records excluded from event totals; run doctor for reconciliation.", summaries))
	}
	text = append(text, "", "MANUAL ACCOUNT OBSERVATIONS (separate scope/currency; never subtracted from local estimates)")
	if len(observations) == 0 {
		text = append(text, "Not entered")
	}
	for _, o := range observations {
		text = append(text, fmt.Sprintf("%s..%s %s %s %s scope=%s", o.PeriodStart, o.PeriodEnd, o.Currency, o.Amount, o.Kind, o.Scope))
	}
	alerts, err := s.AlertStatuses(rates, today())
	if err != nil {
		return nil, err
	}
	for _, a := range alerts {
		if a.Level == "warning" || a.Level == "exceeded" {
			text = append(text, fmt.Sprintf("WARNING: %s: USD %s of %s (%s%%) since %s (NOT a spend cap)",
				a.Label, a.SpentUSD.Fixed(2, false), a.LimitUSD, pyFloat(float64(a.Percent)), a.WindowStart))
		}
	}
	return text, nil
}

// Observation is an amount read from the Meta account by hand, kept apart from local estimates.
type Observation struct {
	ID          int64  `json:"id"`
	ObservedAt  string `json:"observed_at"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope"`
}

// Observations returns the most recent observations first when limit > 0, or all of them in
// insertion order when limit is 0.
func (s *Store) Observations(limit int) ([]Observation, error) {
	query := "SELECT id, observed_at, period_start, period_end, amount, currency, kind, scope FROM observations"
	if limit > 0 {
		query += fmt.Sprintf(" ORDER BY observed_at DESC LIMIT %d", limit)
	}
	rows, err := s.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Observation{}
	for rows.Next() {
		var o Observation
		var fields [7]sql.NullString
		if err := rows.Scan(&o.ID, &fields[0], &fields[1], &fields[2], &fields[3], &fields[4], &fields[5], &fields[6]); err != nil {
			return nil, err
		}
		o.ObservedAt, o.PeriodStart, o.PeriodEnd, o.Amount = fields[0].String, fields[1].String, fields[2].String, fields[3].String
		o.Currency, o.Kind, o.Scope = fields[4].String, fields[5].String, fields[6].String
		result = append(result, o)
	}
	return result, rows.Err()
}
