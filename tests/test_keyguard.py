import json
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

import test_core
from test_core import event
from meta_cost_center import keyguard
from meta_cost_center.webapp import DashboardServer


def fake_keyguard(folder, status, code=0):
    """A stand-in `keyguard` command that prints a fixed `status --json`."""
    script = Path(folder) / 'keyguard'
    script.write_text(f'#!{sys.executable}\nimport sys\nassert sys.argv[1:] == ["status", "--json"]\n'
                      f'print({json.dumps(json.dumps(status))})\nsys.exit({code})\n')
    script.chmod(0o700)
    return {'keyguard_command': str(script)}


def kg_status(**changes):
    status = {'keyguard_version': '0.1.0', 'bridge_running': True,
              'cost_center': {'enabled': True, 'last_check': {'ok': True, 'at': '2026-10-25T12:00:00+00:00'}},
              'last_failure': None,
              'keys': [{'label': 'bootcamp-api', 'state': 'active', 'meta_name': 'bootcamp-api-kg1',
                        'expires_at': '2026-10-25T20:00:00+00:00', 'overdue': False, 'auto_rotate': False, 'last_error': None}],
              'targets': [{'name': 'bootcamp-api', 'managed': True}, {'name': 'bootcamp-web', 'managed': True},
                          {'name': 'Muse Code', 'managed': False}],
              'targets_as_of': '2026-10-25T12:00:00+00:00', 'revocations': []}
    status.update(changes)
    return status


def alert(**changes):
    return {'id': 1, 'revoke_keys': True, 'key_match': 'bootcamp-*', 'window_start': date.today().isoformat(),
            'level': 'exceeded', **changes}


class KeyguardTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        keyguard._cache.clear()

    def tearDown(self):
        self.tmp.cleanup()

    def test_alerts_without_key_revocation_never_run_keyguard(self):
        config = {'keyguard_command': str(Path(self.tmp.name) / 'missing')}
        alerts = [{'id': 1, 'revoke_keys': False}]
        self.assertEqual(keyguard.annotate(alerts, config), [{'id': 1, 'revoke_keys': False}])
        self.assertEqual(keyguard._cache, {})

    def test_enforced_alert_lists_the_keys_it_covers(self):
        config = fake_keyguard(self.tmp.name, kg_status())
        [a] = keyguard.annotate([alert()], config)
        self.assertEqual(a['keyguard']['matching'], ['bootcamp-api', 'bootcamp-web'])
        self.assertTrue(a['keyguard']['enforced'])
        self.assertNotIn('revoked', a['keyguard'])

    def test_revocations_count_only_in_the_current_window(self):
        now = datetime.now(timezone.utc)
        config = fake_keyguard(self.tmp.name, kg_status(revocations=[
            {'at': (now - timedelta(days=3)).isoformat(), 'alert_id': 1, 'keys': ['old-key']},
            {'at': now.isoformat(), 'alert_id': 1, 'keys': ['bootcamp-api']},
            {'at': now.isoformat(), 'alert_id': 2, 'keys': ['other-alert']}]))
        [a] = keyguard.annotate([alert()], config)
        self.assertEqual(a['keyguard']['revoked']['keys'], ['bootcamp-api'])

    def test_reasons_an_alert_is_not_enforced(self):
        cases = [
            (lambda: fake_keyguard(self.tmp.name, kg_status(cost_center={'enabled': False, 'last_check': None})), 'check is off'),
            (lambda: fake_keyguard(self.tmp.name, kg_status(cost_center={'enabled': True, 'last_check': {'ok': False, 'error': 'boom'}})), 'boom'),
            (lambda: fake_keyguard(self.tmp.name, {}, code=1), 'Keyguard failed'),
            (lambda: {'keyguard_command': str(Path(self.tmp.name) / 'missing')}, 'not found'),
        ]
        for make_config, reason in cases:
            keyguard._cache.clear()
            config = make_config()
            [a] = keyguard.annotate([alert()], config)
            self.assertFalse(a['keyguard']['enforced'], reason)
            self.assertIn(reason, a['keyguard']['reason'])

    def test_bridge_down_is_enforced_with_a_warning(self):
        config = fake_keyguard(self.tmp.name, kg_status(bridge_running=False))
        info = keyguard.annotate([alert()], config)[0]['keyguard']
        self.assertTrue(info['enforced'])
        self.assertIn('bridge is not running', info['warning'])

    def test_preview_and_cache(self):
        config = fake_keyguard(self.tmp.name, kg_status())
        self.assertEqual(keyguard.summary(config, 'Muse*, bootcamp-web')['preview'], ['bootcamp-web', 'Muse Code'])
        self.assertEqual(keyguard.summary(config, 'nothing-*')['preview'], [])
        with self.assertRaises(ValueError):
            keyguard.summary(config, 'bad key!')
        # A changed status is not seen until the cache expires.
        fake_keyguard(self.tmp.name, kg_status(targets=[]))
        self.assertEqual(len(keyguard.summary(config)['targets']), 3)
        clock = [0]
        keyguard._cache.clear()
        keyguard.status(config, now=lambda: clock[0])
        clock[0] = keyguard.CACHE_SECONDS + 1
        self.assertEqual(keyguard.status(config, now=lambda: clock[0])['targets'], [])


class KeyguardWebTests(unittest.TestCase):
    write = test_core.CollectorTests.write

    def setUp(self):
        test_core.CollectorTests.setUp(self)
        keyguard._cache.clear()
        self.write(event())
        config = fake_keyguard(self.root, kg_status())
        self.server = DashboardServer(('127.0.0.1', 0), str(self.root / 'db.sqlite3'), [self.logs], config, self.rates,
                                      {'period': 'all'}, 0.1)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f'http://127.0.0.1:{self.server.server_port}'

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        test_core.CollectorTests.tearDown(self)

    def get(self, path):
        with urllib.request.urlopen(self.url + path, timeout=10) as response:
            return json.loads(response.read())

    def post(self, path, body):
        request = urllib.request.Request(self.url + path, data=json.dumps(body).encode(), method='POST',
                                         headers={'Content-Type': 'application/json'})
        with urllib.request.urlopen(request, timeout=10) as response:
            return json.loads(response.read())

    def test_alerts_and_dashboard_carry_keyguard_state(self):
        self.post('/api/alerts', {'period': 'day', 'limit_usd': '5', 'revoke_keys': True, 'key_match': 'bootcamp-api'})
        self.post('/api/alerts', {'period': 'day', 'limit_usd': '5'})
        listed = self.get('/api/alerts')
        self.assertEqual(listed[0]['keyguard']['matching'], ['bootcamp-api'])
        self.assertNotIn('keyguard', listed[1])
        self.assertEqual(self.get('/api/dashboard')['alerts'][0]['keyguard']['enforced'], True)

    def test_keyguard_endpoint_previews_and_rejects_bad_patterns(self):
        summary = self.get('/api/keyguard?match=' + urllib.parse.quote('bootcamp-*'))
        self.assertEqual((summary['installed'], summary['enforced'], summary['preview']), (True, True, ['bootcamp-api', 'bootcamp-web']))
        self.assertEqual(summary['keys'][0]['label'], 'bootcamp-api')
        with self.assertRaises(urllib.error.HTTPError) as error:
            self.get('/api/keyguard?match=' + urllib.parse.quote('bad key!'))
        self.assertEqual(error.exception.code, 400)
        self.assertIn('Key pattern', json.loads(error.exception.read())['error'])
        error.exception.close()


if __name__ == '__main__':
    unittest.main()
