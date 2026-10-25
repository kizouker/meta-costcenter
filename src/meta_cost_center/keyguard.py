"""What Meta Keyguard is doing about alerts that revoke API keys.

Cost Center never changes keys itself. It reads `keyguard status --json`, which holds no
secrets, to show whether those alerts are enforced, which keys they cover, and what Keyguard
has revoked. Keyguard is optional: without it, these alerts are shown as not enforced.
"""
import json
import shlex
import subprocess
import threading
import time
from datetime import date, datetime
from fnmatch import fnmatchcase

from .alerts import key_patterns

# The dashboard polls every two seconds, so Keyguard's answer is reused for a short while.
CACHE_SECONDS = 10
MISSING_CACHE_SECONDS = 60
TIMEOUT = 10

_lock = threading.Lock()
_cache = {}


def command(config):
    return shlex.split(config.get('keyguard_command', 'keyguard'))


def status(config, now=time.monotonic):
    """Keyguard's status, or {'available': False, ...} explaining why it could not be read."""
    argv = command(config)
    key = tuple(argv)
    with _lock:
        cached = _cache.get(key)
        if cached and now() < cached[0]:
            return cached[1]
        result = _read(argv)
        ttl = CACHE_SECONDS if result['available'] or result.get('installed') else MISSING_CACHE_SECONDS
        _cache[key] = (now() + ttl, result)
        return result


def _read(argv):
    try:
        done = subprocess.run(argv + ['status', '--json'], capture_output=True, text=True, timeout=TIMEOUT)
    except FileNotFoundError:
        return {'available': False, 'installed': False,
                'error': f'Meta Keyguard was not found ({argv[0]}). Set keyguard_command in the Cost Center config.'}
    except subprocess.TimeoutExpired:
        return {'available': False, 'installed': True, 'error': 'Meta Keyguard did not answer in time.'}
    if done.returncode != 0:
        lines = (done.stderr or done.stdout).strip().splitlines()
        return {'available': False, 'installed': True,
                'error': f'Meta Keyguard failed: {lines[-1] if lines else done.returncode}'}
    try:
        data = json.loads(done.stdout)
    except json.JSONDecodeError:
        return {'available': False, 'installed': True,
                'error': 'Meta Keyguard is too old to report its status; update it.'}
    return {'available': True, 'installed': True, **data}


def enforcement(kg):
    """Whether Keyguard will act on alerts that revoke keys, and if not, why."""
    if not kg['available']:
        return {'enforced': False, 'reason': kg['error']}
    if not kg['cost_center']['enabled']:
        return {'enforced': False, 'reason': 'Keyguard\'s Cost Center check is off. Run `keyguard cost-center on`.'}
    last = kg['cost_center'].get('last_check') or {}
    if last and not last.get('ok', True):
        return {'enforced': False, 'reason': f'Keyguard cannot run Cost Center: {last.get("error")}'}
    if not kg.get('bridge_running'):
        return {'enforced': True, 'warning': 'Keyguard\'s bridge is not running, so keys cannot be deleted until '
                                             'Chrome is open with the Keyguard extension.'}
    return {'enforced': True}


def matching(kg, key_match):
    """The current keys a key match covers, by Keyguard label or dev.meta.ai name."""
    patterns = key_patterns(key_match).split(',')
    return [t for t in kg.get('targets', []) if any(fnmatchcase(t['name'], p) for p in patterns)]


def _local_day(stamp):
    return datetime.fromisoformat(stamp).astimezone().date()


def annotate(alerts, config):
    """Add Keyguard's view to every alert that revokes keys. Others are left untouched."""
    if not any(a.get('revoke_keys') for a in alerts):
        return alerts
    kg = status(config)
    state = enforcement(kg)
    for a in alerts:
        if not a.get('revoke_keys'):
            continue
        info = dict(state)
        if kg['available']:
            start = date.fromisoformat(a['window_start'])
            # Only revocations in the alert's current window count; a new day or month starts clean.
            done = [r for r in kg.get('revocations', []) if r.get('alert_id') == a['id'] and _local_day(r['at']) >= start]
            if done:
                info['revoked'] = {'at': done[-1]['at'], 'keys': list(dict.fromkeys(k for r in done for k in r.get('keys', [])))}
            info['matching'] = [t['name'] for t in matching(kg, a['key_match'])]
            info['targets_as_of'] = kg.get('targets_as_of')
        a['keyguard'] = info
    return alerts


def summary(config, key_match=None):
    """The Alerts page's Keyguard panel, plus an optional preview of a key match."""
    kg = status(config)
    result = {'available': kg['available'], 'installed': kg.get('installed', False), 'error': kg.get('error'),
              **enforcement(kg)}
    if kg['available']:
        result.update({k: kg.get(k) for k in ('keyguard_version', 'bridge_running', 'last_failure', 'keys',
                                               'targets', 'targets_as_of', 'revocations')})
        result['cost_center_check'] = kg['cost_center']
    if key_match is not None:
        result['preview'] = [t['name'] for t in matching(kg, key_match)] if kg['available'] else None
    return result
