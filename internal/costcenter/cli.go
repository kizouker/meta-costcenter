package costcenter

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// Assets are the files compiled into the binary.
type Assets struct {
	Web     fs.FS
	Pricing []byte
}

// Version is set at release time with -ldflags "-X .../internal/costcenter.Version=<version>".
var Version = "0.1.0-dev"

func init() {
	// `go install ...@v1.2.3` records the module version, which is better than the placeholder.
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasSuffix(Version, "-dev") &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

type option struct {
	name    string // --name
	dest    string
	value   string // metavar; empty for a switch
	repeat  bool
	choices []string
	help    string
}

type command struct {
	name        string
	positionals []string // names of required positionals
	choices     map[string][]string
	options     []option
	help        string
}

var commonOptions = []option{
	{name: "--db", dest: "db", value: "PATH", help: "SQLite database path"},
	{name: "--source", dest: "source", value: "PATH", repeat: true, help: "Session root or JSONL file; repeatable"},
	{name: "--config", dest: "config", value: "PATH", help: "TOML config path"},
	{name: "--prices", dest: "prices", value: "PATH", help: "Versioned pricing JSON path"},
}

var filterOptions = []option{
	{name: "--period", dest: "period", value: "PERIOD", choices: []string{"today", "week", "month", "all"}},
	{name: "--from", dest: "from", value: "DATE"},
	{name: "--to", dest: "to", value: "DATE"},
	{name: "--project", dest: "project", value: "NAME"},
	{name: "--model", dest: "model", value: "NAME"},
	{name: "--billing-mode", dest: "billing_mode", value: "MODE", choices: []string{"payg", "subscription", "unknown"}},
	{name: "--session", dest: "session_filter", value: "ID"},
}

var (
	intervalOption = option{name: "--interval", dest: "interval", value: "SECONDS", help: "Refresh interval (default 15)"}
	onceOption     = option{name: "--once", dest: "once", help: "Scan/render once and exit"}
)

func withFilters(extra ...option) []option {
	return append(append([]option{}, filterOptions...), extra...)
}

var commands = []command{
	{name: "scan", help: "Import new usage from the session logs"},
	{name: "doctor", help: "Show sources, reconciliation, and known limitations as JSON"},
	{name: "watch", options: []option{intervalOption, onceOption}, help: "Rescan and print the report on an interval"},
	{name: "show", options: withFilters(intervalOption,
		option{name: "--port", dest: "port", value: "PORT", help: "Local browser dashboard port (0 selects a free port)"},
		option{name: "--no-browser", dest: "no_browser", help: "Print URL without opening a browser"}),
		help: "Open the live browser dashboard"},
	{name: "tui", options: withFilters(intervalOption, onceOption), help: "Interactive terminal dashboard"},
	{name: "today", options: withFilters(), help: "Today's usage by model"},
	{name: "week", options: withFilters(), help: "The last seven days by model"},
	{name: "month", options: withFilters(), help: "This month by model"},
	{name: "sessions", options: withFilters(option{name: "--sort", dest: "sort", value: "ORDER",
		choices: []string{"session", "estimated-cost"}}), help: "Usage by session"},
	{name: "session", positionals: []string{"session_id"}, options: withFilters(), help: "One session and its subagents"},
	{name: "models", options: withFilters(), help: "Usage by model"},
	{name: "projects", options: withFilters(), help: "Usage by project"},
	{name: "export", options: withFilters(option{name: "--format", dest: "format", value: "FORMAT", choices: []string{"json", "csv"}}),
		help: "Export usage rows as JSON or CSV"},
	{name: "reconcile", positionals: []string{"action"}, choices: map[string][]string{"action": {"add", "list"}}, options: []option{
		{name: "--date", dest: "date", value: "DATE"}, {name: "--from", dest: "from", value: "DATE"},
		{name: "--to", dest: "to", value: "DATE"}, {name: "--amount", dest: "amount", value: "AMOUNT"},
		{name: "--currency", dest: "currency", value: "CODE"},
		{name: "--kind", dest: "kind", value: "KIND", choices: []string{"billed", "outstanding-balance", "usage"}},
		{name: "--scope", dest: "scope", value: "SCOPE"}}, help: "Record or list amounts read from your Meta account"},
	{name: "alert", positionals: []string{"action"}, choices: map[string][]string{"action": {"set", "status"}}, options: []option{
		{name: "--monthly-usd", dest: "monthly_usd", value: "USD"},
		{name: "--project", dest: "project", value: "NAME", help: "Limit the alert to one project (the Muse workspace folder name)"},
		{name: "--at", dest: "at", value: "PERCENTS", help: "Comma-separated warning percentages (default 50,80,100)"},
		{name: "--revoke-keys", dest: "revoke_keys", value: "PATTERN",
			help: "Ask Meta Keyguard to revoke keys matching PATTERN (*, bootcamp-*, or key1,key2) once the limit is exceeded"},
		{name: "--json", dest: "json", help: "Print alert status as JSON (status always prints JSON)"}},
		help: "Add a monthly alert, or print every alert's status as JSON"},
	{name: "hook", positionals: []string{"action"},
		choices: map[string][]string{"action": {"install", "uninstall", "status", "session-start"}}, options: []option{
			{name: "--muse-settings", dest: "muse_settings", value: "PATH", help: "Muse settings file (default ~/.config/muse/settings.json)"}},
		help: "Install the Muse hook that records each session's billing mode"},
	{name: "billing", positionals: []string{"action"}, choices: map[string][]string{"action": {"backfill"}}, options: []option{
		{name: "--mode", dest: "mode", value: "MODE", choices: []string{"payg", "subscription"}, help: "Billing mode to record"},
		{name: "--before", dest: "before", value: "DATE", help: "Only sessions that started before this local date"},
		{name: "--yes", dest: "yes", help: "Record the modes; without it, only list the sessions"}},
		help: "Set the billing mode of sessions from before the hook"},
}

type args struct {
	command     string
	values      map[string][]string
	positionals map[string]string
}

func (a args) has(dest string) bool { _, ok := a.values[dest]; return ok }

func (a args) get(dest string) string {
	if v := a.values[dest]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

type usageError struct {
	message string
	help    bool
}

func (e *usageError) Error() string { return e.message }

func usage() string {
	var b strings.Builder
	b.WriteString("usage: meta-cost <command> [options]\n\nObserve local Muse usage; estimates are not bills.\n\ncommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-10s %s\n", c.name, c.help)
	}
	b.WriteString("\ncommon options:\n")
	for _, o := range commonOptions {
		fmt.Fprintf(&b, "  %-18s %s\n", o.name+" "+o.value, o.help)
	}
	b.WriteString("\nRun `meta-cost <command> --help` for a command's options.\n")
	return b.String()
}

func commandUsage(c command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: meta-cost %s", c.name)
	for _, p := range c.positionals {
		if choices := c.choices[p]; choices != nil {
			fmt.Fprintf(&b, " {%s}", strings.Join(choices, ","))
		} else {
			fmt.Fprintf(&b, " %s", p)
		}
	}
	b.WriteString(" [options]\n\n" + c.help + "\n\noptions:\n")
	for _, o := range append(append([]option{}, c.options...), commonOptions...) {
		name := o.name
		if o.choices != nil {
			name += " {" + strings.Join(o.choices, ",") + "}"
		} else if o.value != "" {
			name += " " + o.value
		}
		fmt.Fprintf(&b, "  %-36s %s\n", name, o.help)
	}
	return b.String()
}

// parseArgs accepts options anywhere, before or after the command, like argparse.
func parseArgs(argv []string) (args, error) {
	a := args{values: map[string][]string{}, positionals: map[string]string{}}
	var spec *command
	var free []string
	findOption := func(name string) *option {
		var pool []option
		if spec != nil {
			pool = spec.options
		}
		for _, list := range [][]option{pool, commonOptions} {
			for i := range list {
				if list[i].name == name {
					return &list[i]
				}
			}
		}
		return nil
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "-h" || arg == "--help" {
			if spec != nil {
				return a, &usageError{commandUsage(*spec), true}
			}
			return a, &usageError{usage(), true}
		}
		if arg == "--version" {
			return a, &usageError{"meta-cost " + Version + "\n", true}
		}
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			if spec == nil {
				for j := range commands {
					if commands[j].name == arg {
						spec = &commands[j]
					}
				}
				if spec == nil {
					return a, &usageError{message: fmt.Sprintf("meta-cost: unknown command %s\n\n%s", pyRepr(arg), usage())}
				}
				a.command = arg
				continue
			}
			free = append(free, arg)
			continue
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		o := findOption(name)
		if o == nil {
			context := "meta-cost"
			if spec != nil {
				context += " " + spec.name
			}
			return a, &usageError{message: fmt.Sprintf("%s: unrecognized argument %s", context, name)}
		}
		if o.value == "" {
			if hasInline {
				return a, &usageError{message: fmt.Sprintf("meta-cost: %s does not take a value", name)}
			}
			a.values[o.dest] = append(a.values[o.dest], "")
			continue
		}
		if !hasInline {
			if i+1 >= len(argv) {
				return a, &usageError{message: fmt.Sprintf("meta-cost: %s needs a value", name)}
			}
			i++
			inline = argv[i]
		}
		if o.choices != nil && !contains(o.choices, inline) {
			return a, &usageError{message: fmt.Sprintf("meta-cost: %s must be one of %s, not %s", name,
				strings.Join(o.choices, ", "), pyRepr(inline))}
		}
		a.values[o.dest] = append(a.values[o.dest], inline)
	}
	if spec == nil {
		return a, &usageError{message: usage()}
	}
	if len(free) != len(spec.positionals) {
		return a, &usageError{message: fmt.Sprintf("meta-cost %s: expected %d argument(s)\n\n%s", spec.name,
			len(spec.positionals), commandUsage(*spec))}
	}
	for i, name := range spec.positionals {
		if choices := spec.choices[name]; choices != nil && !contains(choices, free[i]) {
			return a, &usageError{message: fmt.Sprintf("meta-cost %s: %s must be one of %s, not %s", spec.name, name,
				strings.Join(choices, ", "), pyRepr(free[i]))}
		}
		a.positionals[name] = free[i]
	}
	return a, nil
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// Main runs the CLI and returns its exit status.
func Main(argv []string, assets Assets) int {
	a, err := parseArgs(argv)
	if err != nil {
		var u *usageError
		if errors.As(err, &u) && u.help {
			fmt.Print(u.message)
			return 0
		}
		fmt.Fprintln(os.Stderr, strings.TrimRight(err.Error(), "\n"))
		return 2
	}
	interrupted := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		close(interrupted)
	}()
	if err := run(a, assets, os.Stdout, interrupted); err != nil {
		if errors.Is(err, errInterrupted) {
			return 0
		}
		// Muse runs the SessionStart hook; a failure there must never disturb the session.
		if a.command == "hook" && a.positionals["action"] == "session-start" {
			fmt.Fprintf(os.Stderr, "meta-cost hook: billing mode not recorded: %v\n", err)
			return 0
		}
		fmt.Fprintf(os.Stderr, "meta-cost: %v\n", err)
		return 1
	}
	return 0
}

var errInterrupted = errors.New("interrupted")

func interval(a args) (time.Duration, error) {
	if !a.has("interval") {
		return 15 * time.Second, nil
	}
	seconds, err := strconv.ParseFloat(a.get("interval"), 64)
	if err != nil || seconds < 0.1 || seconds > 86400 {
		return 0, &usageError{message: "Interval must be between 0.1 and 86400 seconds"}
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// decimalAmount is a finite, nonnegative amount, in Python's canonical text.
func decimalAmount(text string) (string, error) {
	d, err := ParseDecimal(text)
	if err != nil {
		return "", fmt.Errorf("Amount must be a number, not %s", pyRepr(text))
	}
	if !d.Finite || d.Value.Sign() < 0 {
		return "", fmt.Errorf("Amount must be finite and nonnegative")
	}
	return d.Text, nil
}

func printJSON(out io.Writer, value any) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	encoder.Encode(value)
	out.Write(buf.Bytes())
}

func run(a args, assets Assets, out io.Writer, interrupted <-chan struct{}) error {
	configPath := defaultConfigPath()
	if a.has("config") {
		configPath = a.get("config")
	}
	config, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	roots := []string{filepath.Join(dataHome(), "muse", "sessions")}
	if config.Sources != nil {
		roots = *config.Sources
	}
	if a.has("source") {
		roots = a.values["source"]
	}
	pricing := assets.Pricing
	pricesPath := ""
	if config.Prices != nil {
		pricesPath = *config.Prices
	}
	if a.has("prices") {
		pricesPath = a.get("prices")
	}
	if pricesPath != "" {
		if pricing, err = os.ReadFile(expandHome(pricesPath)); err != nil {
			return err
		}
	}
	rates, err := LoadPrices(pricing)
	if err != nil {
		return err
	}
	cmd := a.command
	period := a.get("period")
	if period == "" {
		switch cmd {
		case "today", "week", "month":
			period = cmd
		case "sessions", "session", "models", "projects":
			period = "all"
		default:
			period = "today"
		}
	}
	session := a.positionals["session_id"]
	if session == "" {
		session = a.get("session_filter")
	}
	filters := Filters{Period: period, Project: a.get("project"), Model: a.get("model"), Session: session,
		BillingMode: a.get("billing_mode"), Sort: a.get("sort")}
	if cmd != "reconcile" && cmd != "alert" && cmd != "hook" && cmd != "billing" {
		filters.From, filters.To = a.get("from"), a.get("to")
		if _, _, err := bounds(filters); err != nil {
			return err
		}
	}
	dbPath := filepath.Join(dataHome(), "meta-cost-center", "costs.sqlite3")
	if config.DB != nil {
		dbPath = *config.DB
	}
	if a.has("db") {
		dbPath = a.get("db")
	}
	settingsPath := museSettingsPath()
	if a.has("muse_settings") {
		settingsPath = expandHome(a.get("muse_settings"))
	}
	if cmd == "hook" && a.positionals["action"] == "session-start" {
		HookSessionStart(os.Stdin, dbPath, os.Stderr)
		return nil
	}
	store, err := OpenStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if rates, err = store.WithLocalRates(rates); err != nil {
		return err
	}
	switch cmd {
	case "reconcile":
		return reconcile(store, a, out)
	case "alert":
		return alert(store, a, roots, config, rates, out)
	case "hook":
		return hook(store, a, settingsPath, configPath, config, out)
	case "billing":
		return backfill(store, a, roots, config, out)
	case "show":
		port := 8765
		if a.has("port") {
			if port, err = strconv.Atoi(a.get("port")); err != nil {
				return &usageError{message: "--port must be a number"}
			}
		}
		if port < 0 || port > 65535 {
			return fmt.Errorf("Port must be between 0 and 65535")
		}
		every, err := interval(a)
		if err != nil {
			return err
		}
		return RunDashboard(store, roots, config, rates, launchFilters(filters), every, assets.Web, port, !a.has("no_browser"),
			out, interrupted)
	case "tui":
		if !a.has("once") && isTerminal(os.Stdin) && isTerminal(os.Stdout) {
			every, err := interval(a)
			if err != nil {
				return err
			}
			return RunTUI(store, roots, config, filters, rates, every)
		}
	}
	every, err := interval(a)
	if err != nil {
		return err
	}
	for {
		result, err := store.Scan(roots, config.Sessions)
		if err != nil {
			return err
		}
		switch cmd {
		case "scan":
			printJSON(out, result)
		case "doctor":
			sources, err := store.sourceRows()
			if err != nil {
				return err
			}
			reconciliation, err := store.Reconciliation()
			if err != nil {
				return err
			}
			billing, err := store.BillingDiagnostics(settingsPath, config.Sessions)
			if err != nil {
				return err
			}
			printJSON(out, map[string]any{"scan": result, "sources": sources, "reconciliation": reconciliation,
				"pricing_rates": len(rates), "billing_detection": billing, "limitations": []string{"No account-wide coverage", "No supported live billing sync",
					"run.usage upstream shape unverified; unsupported records are diagnosed",
					"Source replacements retain historical observations; provenance may be unavailable"}})
		case "export":
			rows, err := store.Query(filters)
			if err != nil {
				return err
			}
			exported := exportRows(rows, rates)
			if a.get("format") == "csv" {
				if len(exported) > 0 {
					writer := csv.NewWriter(out)
					writer.UseCRLF = true
					writer.Write(exportFields)
					for _, e := range exported {
						writer.Write(e.csvRecord())
					}
					writer.Flush()
				}
			} else {
				printJSON(out, exported)
			}
		default:
			group := map[string]string{"sessions": "session", "session": "session", "projects": "project"}[cmd]
			if group == "" {
				group = "model"
			}
			lines, err := store.Lines(filters, rates, group, &result)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, strings.Join(lines, "\n"))
			if cmd == "tui" {
				fmt.Fprintln(out, "\nInteractive filters require a terminal. Use --project/--model/--session/--from/--to for snapshots.")
			}
		}
		if cmd != "watch" || a.has("once") {
			return nil
		}
		select {
		case <-interrupted:
			return errInterrupted
		case <-time.After(every):
		}
	}
}

// launchFilters are the dashboard's starting filters: only the ones that are set.
func launchFilters(f Filters) map[string]string {
	result := map[string]string{}
	for name, value := range map[string]string{"period": f.Period, "from": f.From, "to": f.To, "project": f.Project,
		"model": f.Model, "session": f.Session, "billing_mode": f.BillingMode} {
		if value != "" {
			result[name] = value
		}
	}
	return result
}

type sourceRow struct {
	Path          string  `json:"path"`
	Identity      *string `json:"identity"`
	Offset        int64   `json:"offset"`
	Anchor        *string `json:"anchor"`
	ScannedAt     *string `json:"scanned_at"`
	Errors        int64   `json:"errors"`
	Pending       int64   `json:"pending"`
	Status        *string `json:"status"`
	ParserVersion int64   `json:"parser_version"`
}

func (s *Store) sourceRows() ([]sourceRow, error) {
	rows, err := s.DB.Query("SELECT path, identity, offset, anchor, scanned_at, errors, pending, status, parser_version FROM sources")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []sourceRow{}
	for rows.Next() {
		var r sourceRow
		var identity, anchor, scanned, status nullText
		if err := rows.Scan(&r.Path, &identity, &r.Offset, &anchor, &scanned, &r.Errors, &r.Pending, &status, &r.ParserVersion); err != nil {
			return nil, err
		}
		r.Identity, r.Anchor, r.ScannedAt, r.Status = identity.ptr(), anchor.ptr(), scanned.ptr(), status.ptr()
		result = append(result, r)
	}
	return result, rows.Err()
}

func reconcile(store *Store, a args, out io.Writer) error {
	if a.positionals["action"] == "add" {
		if !a.has("amount") {
			return fmt.Errorf("--amount is required")
		}
		amount, err := decimalAmount(a.get("amount"))
		if err != nil {
			return err
		}
		date := dateString(today())
		if a.has("date") {
			date = a.get("date")
		}
		start, end := date, date
		if a.get("from") != "" {
			start = a.get("from")
		}
		if a.get("to") != "" {
			end = a.get("to")
		}
		startDay, err := parseDate(start)
		if err != nil {
			return err
		}
		endDay, err := parseDate(end)
		if err != nil {
			return err
		}
		if startDay.After(endDay) {
			return fmt.Errorf("Invalid observation period")
		}
		if _, err := parseDate(date); err != nil {
			return err
		}
		currency := "USD"
		if a.has("currency") {
			currency = a.get("currency")
		}
		if utf8.RuneCountInString(currency) != 3 || strings.IndexFunc(currency, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
			return fmt.Errorf("Currency must be a three-letter code")
		}
		kind, scope := "billed", "account"
		if a.has("kind") {
			kind = a.get("kind")
		}
		if a.has("scope") {
			scope = a.get("scope")
		}
		if _, err := store.DB.Exec("INSERT INTO observations VALUES (NULL,?,?,?,?,?,?,?)", date, start, end, amount,
			strings.ToUpper(currency), kind, scope); err != nil {
			return err
		}
	}
	observations, err := store.Observations(0)
	if err != nil {
		return err
	}
	printJSON(out, observations)
	return nil
}

func alert(store *Store, a args, roots []string, config Config, rates []Rate, out io.Writer) error {
	if a.positionals["action"] == "status" {
		// Meta Keyguard polls this to decide which keys to revoke, so it reads fresh usage first.
		if _, err := store.Scan(roots, config.Sessions); err != nil {
			return err
		}
		statuses, err := store.AlertStatuses(rates, today())
		if err != nil {
			return err
		}
		printJSON(out, statuses)
		return nil
	}
	if !a.has("monthly_usd") {
		return fmt.Errorf("--monthly-usd is required")
	}
	value, err := decimalAmount(a.get("monthly_usd"))
	if err != nil {
		return err
	}
	at := "50,80,100"
	if a.has("at") {
		at = a.get("at")
	}
	seen := map[int]bool{}
	var levels []int
	for _, part := range strings.Split(at, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return fmt.Errorf("invalid literal for int() with base 10: %s", pyRepr(part))
		}
		if !seen[n] {
			seen[n] = true
			levels = append(levels, n)
		}
	}
	sortInts(levels)
	var below []any
	for _, n := range levels {
		if n <= 0 || n > 100 {
			return fmt.Errorf("Warning percentages must be between 1 and 100")
		}
		if n < 100 && len(below) < maxWarnings {
			below = append(below, json.Number(strconv.Itoa(n)))
		}
	}
	if len(below) == 0 {
		below = []any{json.Number("80")}
	}
	body := map[string]any{"period": "month", "limit_usd": value, "warn_levels": below, "revoke_keys": a.has("revoke_keys")}
	if a.get("project") != "" {
		body["project"] = a.get("project")
	}
	if a.has("revoke_keys") {
		body["key_match"] = a.get("revoke_keys")
	}
	if _, err := store.CreateAlert(body); err != nil {
		return err
	}
	scope := "all projects"
	if a.get("project") != "" {
		scope = "project " + a.get("project")
	}
	action := "Cost Center only warns you; it does not stop usage."
	if a.has("revoke_keys") {
		action = fmt.Sprintf("When it is exceeded, Meta Keyguard revokes keys matching %s, provided its Cost Center check is on "+
			"(`meta-keyguard cost-center on`).", a.get("revoke_keys"))
	}
	fmt.Fprintf(out, "Added a monthly alert for %s: USD %s of estimated local usage, priced at API rates. %s\n"+
		"Manage alerts on the dashboard Alerts page.\n", scope, value, action)
	return nil
}

func hook(store *Store, a args, settingsPath, configPath string, config Config, out io.Writer) error {
	switch a.positionals["action"] {
	case "install":
		command, err := hookCommandLine(store.Path, configPath)
		if err != nil {
			return err
		}
		changed, err := InstallHook(settingsPath, command)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(out, "Installed the billing mode hook in %s (a copy of the previous file is in %s.meta-cost.bak).\n"+
				"Sessions started from now on record whether they use the subscription or an API key.\n", settingsPath, settingsPath)
		} else {
			fmt.Fprintf(out, "The billing mode hook is already installed in %s.\n", settingsPath)
		}
	case "uninstall":
		changed, err := UninstallHook(settingsPath)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(out, "Removed the billing mode hook from %s. New sessions will be recorded as unknown.\n", settingsPath)
		} else {
			fmt.Fprintf(out, "The billing mode hook is not installed in %s.\n", settingsPath)
		}
	case "status":
		diagnostics, err := store.BillingDiagnostics(settingsPath, config.Sessions)
		if err != nil {
			return err
		}
		printJSON(out, diagnostics)
	}
	return nil
}

func backfill(store *Store, a args, roots []string, config Config, out io.Writer) error {
	if !a.has("mode") || !a.has("before") {
		return fmt.Errorf("--mode and --before are required")
	}
	before, err := parseDate(a.get("before"))
	if err != nil {
		return err
	}
	// Fresh usage first, so that sessions the hook has seen since the last scan are not listed.
	if _, err := store.Scan(roots, config.Sessions); err != nil {
		return err
	}
	sessions, err := store.UndetectedBefore(dateString(before))
	if err != nil {
		return err
	}
	var skipped []UndetectedSession
	var chosen []UndetectedSession
	for _, u := range sessions {
		if meta, ok := config.Sessions[u.Session]; ok && meta.BillingMode != nil {
			skipped = append(skipped, u)
		} else {
			chosen = append(chosen, u)
		}
	}
	if len(chosen) == 0 {
		fmt.Fprintf(out, "No sessions before %s are missing a billing mode.\n", dateString(before))
		return nil
	}
	if !a.has("yes") {
		for _, u := range chosen {
			fmt.Fprintf(out, "%s  %s  %s\n", localDay(u.Started), u.Session, u.Project)
		}
		fmt.Fprintf(out, "\n%d session(s) would be recorded as %s, with their subagents. Run again with --yes to record them.\n",
			len(chosen), a.get("mode"))
		return nil
	}
	if err := store.Backfill(chosen, a.get("mode")); err != nil {
		return err
	}
	if _, err := store.Scan(roots, config.Sessions); err != nil {
		return err
	}
	fmt.Fprintf(out, "Recorded %d session(s) as %s.\n", len(chosen), a.get("mode"))
	if len(skipped) > 0 {
		fmt.Fprintf(out, "Left %d session(s) alone because the config file sets their billing mode.\n", len(skipped))
	}
	return nil
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

type nullText struct {
	s     string
	valid bool
}

func (n *nullText) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		n.valid = false
	case []byte:
		n.s, n.valid = string(v), true
	default:
		n.s, n.valid = fmt.Sprint(v), true
	}
	return nil
}

func (n nullText) ptr() *string {
	if !n.valid {
		return nil
	}
	return &n.s
}
