import os
import sqlite3
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
    ''')
    return db
