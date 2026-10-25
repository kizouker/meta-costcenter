import json
import os
import sqlite3
from datetime import datetime, timezone
from pathlib import Path


def connect(path):
    path = Path(path).expanduser()
    if not path.parent.exists():
        path.parent.mkdir(parents=True, mode=0o700)
    if not path.exists():
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        os.close(fd)
    db = sqlite3.connect(path, timeout=10)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA journal_mode=WAL")
    db.executescript('''
    CREATE TABLE IF NOT EXISTS sources (
      path TEXT PRIMARY KEY, identity TEXT, offset INTEGER DEFAULT 0,
      anchor TEXT, scanned_at TEXT, errors INTEGER DEFAULT 0,
      pending INTEGER DEFAULT 0, status TEXT, parser_version INTEGER DEFAULT 1);
    CREATE TABLE IF NOT EXISTS events (
      id TEXT PRIMARY KEY, session TEXT, parent TEXT, project TEXT,
      recorded_at TEXT, model TEXT, billing_mode TEXT, input INTEGER,
      output INTEGER, cached INTEGER, source TEXT, sequence TEXT,
      usage_status TEXT);
    CREATE INDEX IF NOT EXISTS events_time ON events(recorded_at);
    CREATE INDEX IF NOT EXISTS events_session ON events(session);
    CREATE TABLE IF NOT EXISTS metadata (source TEXT PRIMARY KEY, project TEXT, model TEXT);
    CREATE TABLE IF NOT EXISTS summaries (
      id TEXT PRIMARY KEY, session TEXT, recorded_at TEXT, model TEXT,
      input INTEGER, output INTEGER, cached INTEGER, source TEXT);
    CREATE TABLE IF NOT EXISTS observations (
      id INTEGER PRIMARY KEY, observed_at TEXT, period_start TEXT,
      period_end TEXT, amount TEXT, currency TEXT, kind TEXT, scope TEXT);
    CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
    -- Rates entered on this machine for models without public pricing. Never exported to the repo.
    CREATE TABLE IF NOT EXISTS rates (
      model TEXT PRIMARY KEY, input_per_million TEXT, cached_input_per_million TEXT,
      output_per_million TEXT, updated_at TEXT);
    CREATE TABLE IF NOT EXISTS alerts (
      id INTEGER PRIMARY KEY, period TEXT, project TEXT, billing_mode TEXT,
      limit_usd TEXT, warn_levels TEXT DEFAULT '[80]', enabled INTEGER DEFAULT 1, created_at TEXT,
      revoke_keys INTEGER DEFAULT 0, key_match TEXT);
    ''')
    migrate_warn_levels(db)
    migrate_key_revocation(db)
    migrate_monthly_warning(db)
    return db


def migrate_warn_levels(db):
    """Alerts first had one warning percentage; they now keep a JSON list of up to four."""
    columns = {r[1] for r in db.execute('PRAGMA table_info(alerts)')}
    if 'warn_percent' not in columns:
        return
    with db:
        if 'warn_levels' not in columns:
            db.execute("ALTER TABLE alerts ADD COLUMN warn_levels TEXT DEFAULT '[80]'")
        db.execute('UPDATE alerts SET warn_levels = json_array(warn_percent)')
        db.execute('ALTER TABLE alerts DROP COLUMN warn_percent')


def migrate_key_revocation(db):
    """Alerts can ask Meta Keyguard to revoke matching API keys once their limit is exceeded."""
    columns = {r[1] for r in db.execute('PRAGMA table_info(alerts)')}
    with db:
        if 'revoke_keys' not in columns:
            db.execute('ALTER TABLE alerts ADD COLUMN revoke_keys INTEGER DEFAULT 0')
        if 'key_match' not in columns:
            db.execute('ALTER TABLE alerts ADD COLUMN key_match TEXT')


def migrate_monthly_warning(db):
    """The CLI used to store one monthly threshold in settings; it is now an ordinary alert."""
    row = db.execute("SELECT value FROM settings WHERE key='monthly_usd'").fetchone()
    if not row:
        return
    levels = db.execute("SELECT value FROM settings WHERE key='alert_levels'").fetchone()
    below = sorted({level for level in (json.loads(levels[0]) if levels else []) if 0 < level < 100})[:4]
    with db:
        db.execute('INSERT INTO alerts (period, project, billing_mode, limit_usd, warn_levels, enabled, created_at) '
                   "VALUES ('month', NULL, NULL, ?, ?, 1, ?)",
                   (row[0], json.dumps(below or [80]), datetime.now(timezone.utc).isoformat()))
        db.execute("DELETE FROM settings WHERE key IN ('monthly_usd', 'alert_levels')")
