# Billing Mode Detection — Plan

> **Status:** Implemented · **Created:** 2026-10-25 · **Implemented:** 2026-10-26
> **Parent plan:** [PLAN.md](PLAN.md) · **Builds on:** [COST-AND-ALERTS.md](COST-AND-ALERTS.md)

## 1. Problem

Every event in the database has `billing_mode = 'unknown'`, because Muse does not write
the credential it used into its session logs and the collector therefore has nothing to
read. The only way to change the mode today is a `[sessions."<id>"]` entry in
`config.toml`, which nobody maintains by hand for every new session. As a result, any
alert scoped to "pay as you go only" matches no events and reports $0.00, even on a day
like 2026-10-25 when local usage was about $0.70 across three projects.

Cost Center should record, for each session, whether it was paid through the Meta
account subscription or through an API key, and, when the key came from Meta Keyguard,
which key it was. Detection must work when Keyguard is not installed, since Cost Center
is a standalone tool and Keyguard is an optional companion.

## 2. Findings

These were established on 2026-10-25 against Muse 1.4.13 (`muse-bin-1.4.13-R6996.1`),
using `muse exec --provider echo`, which runs a session without calling a model.

- **Muse chooses a credential in a fixed order.** A `META_API_KEY` environment variable
  wins, then an API key stored with `muse auth`, and then the account login, which mints
  a key from the subscription. The binary's own strings describe this as "META_API_KEY
  always takes priority over the account login", and it names the lanes `envKey`, the
  stored API-key slot, and `accountLogin`. The chosen lane is not logged anywhere: it is
  absent from the session JSONL, `local-tracing/`, and `session-index.db`.
- **`~/.config/muse/auth.json` describes the account, not a session.** It holds
  `mechanism` (`oauth` for the account login), `obtained_via`, and `subs_active`,
  alongside secrets that Cost Center must never read. Because it is a single file that
  changes on login and logout, it can tell us what the fallback lane would be at session
  start, but not whether an environment key overrode it. A key stored with
  `muse auth set --api-key-stdin` produces a different shape: the `meta` entry contains
  only `api_key`, with no `mechanism`, `access_token`, or `subs_active`. This was
  confirmed by storing a dummy key with `XDG_CONFIG_HOME` pointed at a temporary
  directory, which left the real `auth.json` unchanged.
- **Muse supports Claude Code–style hooks, and `SessionStart` fires for every session.**
  The hook receives JSON on stdin containing `session_id`, `cwd`, `hook_event_name`, and
  `source` (`startup` or `resume`), and its environment contains `MUSE_SESSION_ID`. The
  session ID matches the `session` column in `events`, so a detection can be joined to
  usage without any further mapping.
- **Muse removes `META_API_KEY` from the hook's environment**, so the hook cannot test
  the variable directly. However, the hook runs as the same user as Muse, which means it
  can read `/proc/<pid>/environ` of the ancestor process whose `comm` starts with
  `muse`. In testing this reported the variable as present when Muse was started with a
  dummy key and absent when it was not. The hook only tests for the name and never reads
  the value.
- **Project-level hooks are ignored in untrusted workspaces.** Hooks therefore have to
  be installed in the user-level `~/.config/muse/settings.json`, which applies to every
  workspace. Hooks in that file run without a review step in both `muse exec` and the
  TUI.
- **Muse runs hooks with a reduced environment.** Besides `META_API_KEY`, it removes
  `XDG_DATA_HOME` and `XDG_CONFIG_HOME`, so a hook that relied on them would write to the
  default database and read the default `auth.json` rather than the ones Muse is using.
  The installed hook command therefore names the database and config file explicitly,
  and the hook locates `auth.json` from the Muse process's own `XDG_CONFIG_HOME` or
  `HOME` (§4.2).
- **Plugin hooks are the alternative route.** A native plugin bundle with a
  `SessionStart` hook (`.muse-plugin/plugin.json`, installed with
  `muse plugins install --scope user` and approved with `muse plugins approve`) also
  works, and Muse treats it as the reviewed path for third-party hooks. It was not
  chosen because every change to the hook's definition, including a moved binary,
  requires approving it again. Muse's binary refers to settings-file hooks as
  "legacy", so if a future release stops running them, moving to a plugin is the
  fallback, and §8 is how that change would be noticed.
- **Keyguard does not currently identify the key it supplies.** `meta-keyguard exec`
  replaces `META_API_KEY` in the environment and execs the command, and
  `meta-keyguard env` prints an `export` line, but neither sets anything naming the key.

## 3. Decisions

| Question | Decision |
|---|---|
| Where detection lives | In Cost Center, as a `SessionStart` hook command, so that it works with or without Keyguard. |
| What is recorded | Billing mode, how it was determined, and the Keyguard key label when one is present. Never a secret, a key fingerprint, or any other part of `auth.json`. |
| Precedence | An explicit `[sessions]` entry in `config.toml` still wins over a detection, so that a user can correct a wrong guess. |
| Sessions from before the hook existed | They stay `unknown`. A one-time backfill command is offered separately (§7) rather than guessed silently. |
| Hook failure | The hook always exits 0 and never blocks Muse; a failure leaves the session `unknown`, and `doctor` reports it. |

## 4. Detection

### 4.1 Hook installation

`meta-cost hook install` adds one entry to `~/.config/muse/settings.json`:

```json
{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "<absolute path to meta-cost> hook session-start"}]}]}}
```

The command is the absolute path of the running binary followed by
`hook session-start --db <path> --config <path>`, so the hook keeps working when
`~/.local/bin` is not on Muse's `PATH` and writes to the same database as the dashboard
even though Muse removes the XDG variables from its environment. Installation merges
into the existing file rather than replacing it, keeps any other hooks, is idempotent,
updates an existing entry rather than adding a second one when the binary has moved,
and writes a backup to `settings.json.meta-cost.bak` before the first change. The backup
is named for this tool so that it never overwrites a backup of Muse's own. `meta-cost hook uninstall` removes only the
entry it added, and `meta-cost hook status` prints whether the entry is present and
when the last detection was recorded. The dashboard does not install the hook itself,
because editing another tool's configuration should be an explicit action by the user.

### 4.2 Deciding the mode

`meta-cost hook session-start` reads the hook JSON from stdin and walks up the process
tree from its parent until it finds a process whose `comm` begins with `muse`. It then
applies the following rules in order and records the first that matches:

1. `META_API_KEY` is present and non-empty in that process's `/proc/<pid>/environ` →
   `payg`, source `env_key`.
2. `--api-key-stdin` appears in that process's `/proc/<pid>/cmdline` → `payg`, source
   `stdin_key`.
3. `auth.json` has a `providers.meta` entry with no `mechanism` field → `payg`, source
   `stored_key`. This is the shape `muse auth set --api-key-stdin` writes (§2), whereas
   the account login always writes `mechanism = "oauth"`. The hook checks only that the
   `api_key` field is present and non-empty, without reading its value.
4. `auth.json` has `mechanism = "oauth"` and `subs_active = true` → `subscription`,
   source `account_login`.
5. Otherwise → `unknown`, source `undetermined`.

When rule 1 matches and the same environment contains `META_KEYGUARD_KEY` (§6), its
value is recorded as the key label. `auth.json` is read from the Muse process's
`XDG_CONFIG_HOME`, or from its `HOME` when that is unset, because the hook's own
environment lacks both. The hook parses `auth.json` with a decoder that
reads only `mechanism` and `subs_active` into a struct, so that the secrets in the file
are never held in a named variable or written anywhere.

The whole hook runs under a two-second deadline. If any step fails, such as the Muse
process not being found or `/proc` being unreadable, it records `unknown` with the
error as the source detail where possible, and exits 0 in every case.

### 4.3 Storage

Detections go into a new table in the existing database:

```sql
CREATE TABLE IF NOT EXISTS session_billing (
  session TEXT, detected_at TEXT, billing_mode TEXT, source TEXT,
  key_label TEXT, hook_source TEXT,
  PRIMARY KEY (session, detected_at));
```

A session can have more than one row because a resumed session fires `SessionStart`
again with `source = "resume"`, and the credential may differ between the original run
and the resume (for example, a session started on the subscription and resumed under
`meta-keyguard exec`). The hook opens the database with a short busy timeout so that it
does not stall Muse while a scan holds the write lock; if the write still fails, the
detection is lost and the session stays `unknown`.

## 5. Applying detections to usage

During a scan, after events are ingested and before the `[sessions]` config overrides
are applied, the collector sets each event's `billing_mode` from `session_billing`:

- For an event in a session that has detections, the mode is taken from the latest
  detection whose `detected_at` is at or before the event's `recorded_at`. Events
  recorded before the first detection use the first detection, since the hook fires
  before the first model call.
- For a subagent, observer, or approval-review event (one with a `parent`), the
  subagent's own detection is used if one exists, and otherwise the parent session's
  detection is used, because subagents run inside the parent Muse process and share
  its credential.
- `[sessions."<id>"] billing_mode` in `config.toml` is applied afterwards and therefore
  overrides any detection, as it does today.

Events gain a `key_label` column, which exports include, so that spend can be traced to
the Keyguard key that paid for it. The session page shows each detection under the
billing mode, with how it was determined, the key label, and whether it came from a
resume, so that a user can tell a detected `subscription` from a configured one.

Adding the column changes the shape of `events`, and binaries from before this change
insert events positionally, so a dashboard left running from an older release fails to
import new usage once a newer binary has opened the database. Running dashboards should
therefore be restarted after upgrading.

## 6. Keyguard change

`meta-keyguard exec` and `meta-keyguard env` additionally set
`META_KEYGUARD_KEY=<label>`, where the label is the key's name in the Keyguard store.
The label is not a secret, and it is the same name that alert `key_match` patterns are
written against, which means an alert's spend can later be compared with the keys it
would revoke. The variable name is fixed rather than following the `env_var` setting,
because Cost Center needs a stable name to look for. Without Keyguard, or with a key
exported by hand, rule 1 still yields `payg` and only the label is missing.

## 7. Backfilling past sessions

Sessions recorded before the hook was installed have no detection and remain
`unknown`. `meta-cost billing backfill --mode subscription --before <date>` lists every
top-level session with no detection that started before the given local date, and
with `--yes` records the given mode for them with the source `backfill`, dated at each
session's first event so that it covers all of its usage. Subagents follow their
parent, and sessions whose billing mode is set in the config file are left alone. It
is never run automatically, because for most users the honest answer for old sessions
is "unknown".

## 8. Visibility of failures

- `meta-cost doctor` reports whether the hook is installed, when the last detection was
  recorded, and how many sessions in the last seven days have no detection. A session
  with usage but no detection, after the hook was installed, most likely means a Muse
  update stopped running or changed hooks.
- Alert statuses gain `unknown_billing_events`, the number of completions in the window
  that a pay-as-you-go alert cannot count because their billing mode is unknown. The
  Alerts page shows the count and a notice that names `meta-cost hook install`.

## 9. Limits

- Detection relies on `/proc`, so it works only on Linux and WSL. This matches the
  platforms Cost Center already supports.
- Muse hooks and the `SessionStart` payload are not documented by Meta and may change in
  a later release. §8 exists so that such a change shows up as a warning rather than as
  silently `unknown` usage.
- A key supplied in a way other than the four rules above, such as a future Muse login
  mechanism, is recorded as `unknown` rather than guessed.
- The subscription rule reflects the account's state at session start. If a
  subscription lapses mid-session and Muse falls back to another lane, the change is
  not visible until the next `SessionStart`.

## 10. Testing

- Unit tests for the rule order in §4.2, using a fake process tree and fake
  `environ`, `cmdline`, and `auth.json` files under a temporary directory, including a
  test that an `auth.json` containing secrets produces a record with no secret text in
  it.
- Unit tests for §5, covering resumed sessions with two detections, subagents
  inheriting from their parent, and config overrides taking precedence.
- Tests that `hook install` preserves unrelated hooks and settings, is idempotent, and
  that `hook uninstall` removes only its own entry.
- A manual end-to-end check with `muse exec --provider echo`, which costs nothing, in a
  temporary `XDG_CONFIG_HOME` and `XDG_DATA_HOME` and with every `meta-cost` path given
  explicitly. On 2026-10-26 this recorded `subscription`/`account_login` without a key
  and `payg`/`env_key` with the label `day-zero-test` when `META_API_KEY` and
  `META_KEYGUARD_KEY` were set. The `--api-key-stdin` rule could not be checked this way,
  because Muse validates the key before the session starts and rejects a dummy one, so
  it is covered by unit tests only.
- A test that an alert with `billing_mode = 'payg'` reports the spend of an event whose
  session was detected as `payg`.

## 11. Privacy

The hook reads the names of environment variables from the Muse process and two
non-secret fields from `auth.json`. It never reads or stores an API key, access token,
key fingerprint, email address, or prompt content, and it sends nothing over the
network. The only new data stored is the billing mode, its source, and the Keyguard key
label, all in the existing local database.
