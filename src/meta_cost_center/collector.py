"""Version 1 adapter: native envelope -> payload.event.model_completed.

Only allowlisted usage fields reach SQLite. Transcript fields are never persisted.
"""
import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path

PARSER_VERSION = 1


def now():
    return datetime.now(timezone.utc).isoformat()


def timestamp(value):
    if isinstance(value, (float, int)):
        if value > 100_000_000_000_000:  # native Muse microseconds
            value /= 1_000_000
        elif value > 100_000_000_000:
            value /= 1000
        return datetime.fromtimestamp(value, timezone.utc).isoformat()
    if isinstance(value, str):
        dt = datetime.fromisoformat(value.replace('Z', '+00:00'))
        if dt.tzinfo is None:
            raise ValueError('timestamp requires timezone')
        return dt.astimezone(timezone.utc).isoformat()
    raise ValueError('missing timestamp')


def count(value):
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError('invalid token count')
    return value


def tokens(usage):
    inp, out = count(usage['input_tokens']), count(usage['output_tokens'])
    cached = usage.get('cached_tokens', usage.get('cached_input_tokens', usage.get('cache_read_tokens')))
    if cached is not None:
        cached = count(cached)
        if cached > inp:
            raise ValueError('cached tokens exceed input')
    return inp, out, cached


def identity(path):
    stat = path.stat()
    return f'{stat.st_dev}:{stat.st_ino}'


def anchor_at(handle, offset):
    # Anchor normalized record metadata, never transcript bytes or their hashes.
    if not offset:
        return ''
    pos = max(0, offset - 4096)
    while True:
        handle.seek(pos)
        data = handle.read(offset - pos).rstrip(b'\n')
        split = data.rfind(b'\n')
        if split >= 0 or pos == 0:
            break
        pos = max(0, pos - 65536)
    try:
        record = json.loads(data[split+1:])
        return json.dumps([record.get('id'), record.get('sequence'), record.get('recorded_at')])
    except (ValueError, AttributeError):
        return 'malformed-record'


def session_info(path):
    parts = path.parts
    session = path.parent.name
    parent = None
    for label in ('subagent', 'observer', 'approval-review'):
        if label in parts:
            i = len(parts) - 1 - parts[::-1].index(label)
            if i and i + 1 < len(parts):
                parent = parts[i - 1]
                session = path.stem if label == 'approval-review' else parts[i + 1]
    return session, parent


def ingest(db, path, project='unassigned', billing_mode='unknown'):
    path = path.resolve()
    source = str(path)
    previous = db.execute('SELECT * FROM sources WHERE path=?', (source,)).fetchone()
    file_id = identity(path)
    session, parent = session_info(path)
    metadata = db.execute('SELECT project, model FROM metadata WHERE source=?', (source,)).fetchone()
    detected_project, detected_model = tuple(metadata) if metadata else ('unassigned', 'unknown')
    added = errors = pending = 0
    offset = previous['offset'] if previous else 0
    with path.open('rb') as f, db:
        reset = bool(previous and (previous['identity'] != file_id or path.stat().st_size < offset
                     or anchor_at(f, offset) != previous['anchor']))
        if reset:
            offset = 0
            detected_project, detected_model = 'unassigned', 'unknown'
        # Cumulative malformed-record count survives incremental scans; reset on replacement.
        errors = previous['errors'] if previous and not reset else 0
        f.seek(offset)
        while True:
            start = f.tell()
            line = f.readline()
            if not line:
                break
            if not line.endswith(b'\n'):
                pending = 1
                break
            offset = f.tell()
            try:
                record = json.loads(line)
                payload = record.get('payload', {})
                event = payload.get('event', {})
                if record.get('payload_type') == 'runtime.session.metadata':
                    meta = payload.get('record', {})
                    if isinstance(meta.get('workspace_root'), str):
                        detected_project = Path(meta['workspace_root']).name or 'unassigned'
                    if isinstance(meta.get('model_id'), str):
                        detected_model = meta['model_id']
                    continue
                kind = event.get('kind', record.get('type'))
                if kind not in ('model_completed', 'run.usage'):
                    continue
                recorded_at = timestamp(record.get('recorded_at', record.get('timestamp')))
                # Native IDs survive moves, truncation, and rescans. Fallback uses source+sequence.
                seq = str(record.get('sequence', start))
                stream = record.get('stream', {})
                sid = stream.get('id') or record.get('session_id') or session
                rid = record.get('id') or hashlib.sha256(f'{source}:{sid}:{seq}:{kind}'.encode()).hexdigest()
                if kind == 'run.usage':
                    # Explicit supported interchange shape; unknown upstream shapes are diagnosed.
                    models = record.get('usage', {}).get('models')
                    if not isinstance(models, list) or not models:
                        raise ValueError('unsupported run.usage shape')
                    for m in models:
                        inp, out, cached = tokens(m)
                        db.execute('INSERT OR IGNORE INTO summaries VALUES (?,?,?,?,?,?,?,?)',
                                   (f'{rid}:{m["model"]}', sid, recorded_at, m['model'], inp, out, cached, source))
                    continue
                inp, out, cached = tokens(event['usage'])
                model = event.get('model') or detected_model
                status = 'complete' if cached is not None else 'cache_unknown'
                cursor = db.execute('INSERT OR IGNORE INTO events VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)',
                    (rid, sid, parent, project if project != 'unassigned' else detected_project, recorded_at, model, billing_mode,
                     inp, out, cached, source, seq, status))
                added += cursor.rowcount
            except (ValueError, TypeError, KeyError, AttributeError, OverflowError, OSError):
                errors += 1
        db.execute('INSERT OR REPLACE INTO metadata VALUES (?,?,?)', (source, detected_project, detected_model))
        anchor = anchor_at(f, offset)
        db.execute('''INSERT INTO sources VALUES (?,?,?,?,?,?,?,?,?)
          ON CONFLICT(path) DO UPDATE SET identity=excluded.identity, offset=excluded.offset,
          anchor=excluded.anchor, scanned_at=excluded.scanned_at, errors=excluded.errors,
          pending=excluded.pending, status=excluded.status, parser_version=excluded.parser_version''',
          (source, file_id, offset, anchor, now(), errors, pending,
           'replaced/truncated; historical observations retained' if reset else 'ok', PARSER_VERSION))
    return added


def scan(db, roots, config=None):
    config = config or {}
    paths = set()
    warnings = []
    for root in roots:
        root = Path(root).expanduser()
        if not root.exists():
            warnings.append(f'Source unavailable: {root}')
            continue
        try:
            paths.update([root] if root.is_file() else (p for p in root.rglob('*.jsonl') if p.name == 'session.jsonl' or 'approval-review' in p.parts))
        except OSError:
            warnings.append(f'Cannot discover source: {root}')
    added = 0
    for path in sorted(paths):
        try:
            meta = config.get('sessions', {}).get(session_info(path)[0], {})
            added += ingest(db, path, meta.get('project', 'unassigned'), meta.get('billing_mode', 'unknown'))
        except OSError:
            warnings.append(f'Cannot read source: {path}')
    # Explicit metadata mappings also apply to previously imported observations.
    with db:
        for sid, meta in config.get('sessions', {}).items():
            if 'project' in meta:
                db.execute('UPDATE events SET project=? WHERE session=?', (meta['project'], sid))
            if 'billing_mode' in meta:
                db.execute('UPDATE events SET billing_mode=? WHERE session=?', (meta['billing_mode'], sid))
    if not paths:
        warnings.append('No session logs discovered; coverage unavailable (not zero usage).')
    missing = [r[0] for r in db.execute('SELECT path FROM sources') if Path(r[0]) not in paths]
    if missing:
        warnings.append(f'{len(missing)} previously imported sources outside this scan or no longer available; retained usage remains visible.')
    return {'added': added, 'files': len(paths), 'warnings': warnings, 'scanned_at': now()}
