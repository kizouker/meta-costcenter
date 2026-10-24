"""Optional end-to-end checks: requires Playwright and installed Chrome."""
import json
import shutil
import sys
import tempfile
import threading
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / 'src'), str(ROOT / 'tests')]
from playwright.sync_api import sync_playwright, expect
from meta_cost_center.webapp import DashboardServer
from meta_cost_center.reports import load_prices
from test_core import event


def main():
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        logs = root / 'logs'
        for i, project in enumerate(['Website', 'CLI tools', 'Research', 'Website']):
            path = logs / f'session-{i}' / 'session.jsonl'
            path.parent.mkdir(parents=True)
            records = [{'payload_type': 'runtime.session.metadata', 'payload': {'record': {'workspace_root': f'/demo/{project}'}}}]
            records += [event(f'event-{i}-{j}', f'session-{i}', inp=10000*(i+1), cached=5000, model='muse-spark-1.3') for j in range(3)]
            path.write_text(''.join(json.dumps(r)+'\n' for r in records))
        # A subagent of session-3 with output-only usage, so input totals stay round.
        sub = logs / 'session-3' / 'subagent' / 'sub-3a' / 'session.jsonl'
        sub.parent.mkdir(parents=True)
        sub.write_text(''.join(json.dumps(r)+'\n' for r in [{'payload_type': 'runtime.session.metadata', 'payload': {'record': {'workspace_root': '/demo/Website'}}}]
                               + [event(f'sub-event-{j}', 'sub-3a', inp=0, cached=0, model='muse-spark-1.3') for j in range(2)]))
        rates = load_prices(ROOT / 'src/meta_cost_center/pricing.json')
        server = DashboardServer(('127.0.0.1', 0), root/'usage.sqlite3', [logs], {}, rates, {'period': 'all'}, .1)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with sync_playwright() as p:
                executable = shutil.which('google-chrome') or shutil.which('chromium')
                browser = p.chromium.launch(**({'executable_path': executable} if executable else {}), headless=True, args=['--no-sandbox'])
                page = browser.new_page(viewport={'width': 1440, 'height': 1100})
                errors, documents = [], []
                page.on('pageerror', lambda error: errors.append(str(error)))
                page.on('request', lambda request: documents.append(request.url) if request.resource_type == 'document' else None)
                base = f'http://127.0.0.1:{server.server_port}'
                def open_filters():
                    page.get_by_role('button', name='Filters', exact=True).click()
                    expect(page.get_by_role('dialog', name='Filters')).to_be_visible()
                def close_filters():
                    page.get_by_role('button', name='Close filters').click()
                    expect(page.get_by_role('dialog')).to_have_count(0)
                def check_filter(name, value):
                    open_filters()
                    expect(page.locator(f'[name={name}]')).to_have_value(value)
                    close_filters()
                page.goto(base + '/')
                expect(page.locator('#input')).to_have_text('300,000')
                expect(page.get_by_role('dialog')).to_have_count(0)
                assert page.url == base + '/'
                open_filters()
                expect(page.locator('[name=period]')).to_have_value('all')
                page.keyboard.press('Escape')
                expect(page.get_by_role('dialog')).to_have_count(0)
                expect(page.get_by_role('button', name='Filters', exact=True)).to_be_focused()
                page.get_by_role('button', name='Export usage').hover()
                expect(page.get_by_role('tooltip')).to_contain_text('current filters')
                page.keyboard.press('Escape')
                page.set_viewport_size({'width': 1440, 'height': 420})
                for label, heading, rows in (('Overview', 'Usage overview', '#input'), ('Sessions', 'Sessions', '#session-rows'),
                                             ('Projects', 'Projects', '#project-rows'), ('Breakdown', 'Usage breakdown', '#breakdown-rows')):
                    page.locator('.nav', has_text=label).click()
                    expect(page.locator('h1')).to_have_text(heading)
                    page.wait_for_selector(rows)
                    assert page.locator('.page-header').bounding_box()['height'] <= 80
                    page.evaluate("window.scrollTo({top: 600, behavior: 'instant'})")
                    assert page.evaluate('window.scrollY') > 0
                    assert abs(page.locator('.page-header').bounding_box()['y']) < 2
                    expect(page.get_by_role('button', name='Filters', exact=True)).to_be_in_viewport()
                page.locator('.nav', has_text='Overview').click()
                page.set_viewport_size({'width': 1440, 'height': 1100})
                expect(page.locator('#input')).to_have_text('300,000')
                open_filters()
                page.locator('[name=project]').select_option('Website')
                expect(page.locator('#input')).to_have_text('150,000')
                # Switching projects must keep every project available without clearing first.
                expect(page.locator('[name=project] option')).to_have_count(4)
                page.locator('[name=project]').select_option('Research')
                expect(page.locator('#input')).to_have_text('90,000')
                page.locator('[name=project]').select_option('CLI tools')
                expect(page.locator('#input')).to_have_text('60,000')
                page.locator('[name=project]').select_option('')
                expect(page.locator('#input')).to_have_text('300,000')
                page.locator('[name=project]').select_option('Website')
                expect(page.locator('#input')).to_have_text('150,000')
                page.screenshot(path='/tmp/muse-cost-filters-desktop.png', full_page=True)
                close_filters()
                assert page.url == base + '/'
                page.locator('.nav', has_text='Breakdown').click()
                expect(page.locator('h1')).to_have_text('Usage breakdown')
                check_filter('project', 'Website')
                expect(page.locator('#breakdown-rows tr')).to_have_count(1)
                assert len(documents) == 1
                page.get_by_role('button', name='Token share').hover()
                expect(page.get_by_role('tooltip')).to_contain_text('not a share of cost')
                page.keyboard.press('Escape')
                page.locator('[data-group=session]').click()
                expect(page.locator('#breakdown-rows tr')).to_have_count(3)
                page.reload()
                expect(page.locator('[data-group=session]')).to_have_attribute('aria-pressed', 'true')
                expect(page.get_by_role('dialog')).to_have_count(0)
                check_filter('project', 'Website')
                # Breakdown session rows open the session page instead of silently filtering.
                page.locator('#breakdown-rows a', has_text='session-3').click()
                expect(page).to_have_url(base + '/sessions/session-3')
                expect(page.locator('h1')).to_have_text('Session')
                expect(page.locator('.chip-note')).to_be_visible()
                expect(page.locator('#event-rows tr')).to_have_count(5)
                expect(page.locator('#subagent-tree')).to_contain_text('sub-3a')
                with (logs/'session-3/session.jsonl').open('a') as f:
                    f.write(json.dumps(event('new-live', 'session-3', inp=1234, cached=100, model='muse-spark-1.3'))+'\n')
                expect(page.locator('#event-rows tr')).to_have_count(6, timeout=10000)
                expect(page.locator('#event-rows')).to_contain_text('new-live')
                page.reload()
                expect(page.locator('h1')).to_have_text('Session')
                expect(page.locator('#event-rows tr')).to_have_count(6)
                with page.expect_download() as download:
                    page.locator('#export').click()
                export = json.loads(Path(download.value.path()).read_text())
                assert len(export) == 6 and {row['session'] for row in export} == {'session-3', 'sub-3a'}
                page.locator('#subagent-tree a', has_text='sub-3a').click()
                expect(page).to_have_url(base + '/sessions/sub-3a')
                expect(page.locator('#event-rows tr')).to_have_count(2)
                page.locator('#parent-link').click()
                expect(page).to_have_url(base + '/sessions/session-3')
                page.go_back()
                expect(page).to_have_url(base + '/sessions/sub-3a')
                page.go_forward()
                expect(page).to_have_url(base + '/sessions/session-3')
                # Sessions list: one row per root session; filter chips explain and undo narrowing.
                page.locator('.nav', has_text='Sessions').click()
                expect(page.locator('#session-rows tr')).to_have_count(2)
                expect(page.locator('#session-rows')).not_to_contain_text('sub-3a')
                page.get_by_role('button', name='Remove project filter').click()
                expect(page.locator('#session-rows tr')).to_have_count(4)
                page.locator('#session-rows tr', has_text='Research').locator('td').nth(2).click()
                expect(page).to_have_url(base + '/sessions/session-2')
                page.locator('.breadcrumb a', has_text='Sessions').click()
                open_filters()
                page.locator('[name=session]').fill('No such session')
                expect(page.locator('#session-rows')).to_contain_text('No sessions match')
                page.locator('#clear').click()
                expect(page.locator('#session-rows tr')).to_have_count(4)
                close_filters()
                page.locator('.nav', has_text='Projects').click()
                page.locator('#project-rows a', has_text='Website').click()
                expect(page).to_have_url(base + '/projects/Website')
                expect(page.locator('#session-count')).to_have_text('2')
                expect(page.locator('#input')).to_have_text('151,234')
                # Chart toggle: cost by default, tokens on request.
                page.locator('.nav', has_text='Overview').click()
                expect(page.locator('#chart')).to_have_attribute('aria-label', 'Estimated cost by day')
                page.locator('[data-metric=tokens]').click()
                expect(page.locator('#chart')).to_have_attribute('aria-label', 'Token usage by day')
                page.locator('[data-metric=cost]').click()
                # Alerts: a tiny daily limit is exceeded at once and raises the banner on every page.
                # A stub records desktop notifications, as if permission had been granted.
                page.evaluate('''() => { window.__notes = []; window.Notification = class {
                    static permission = 'granted'; static requestPermission() { return Promise.resolve('granted'); }
                    constructor(title) { window.__notes.push(title); } }; }''')
                page.locator('.nav', has_text='Alerts').click()
                expect(page.locator('#notification-state')).to_contain_text('notifications are on')
                expect(page.get_by_role('button', name='Filters', exact=True)).to_have_count(0)
                # The form opens in a modal; Escape closes it without saving.
                page.locator('#add-alert').click()
                expect(page.get_by_role('dialog', name='Add alert')).to_be_visible()
                page.keyboard.press('Escape')
                expect(page.get_by_role('dialog')).to_have_count(0)
                expect(page.locator('#add-alert')).to_be_focused()
                page.locator('#add-alert').click()
                # Warning 1 is always on; extra warnings are disabled until their box is checked.
                expect(page.locator('#warning-1')).to_be_disabled()
                page.get_by_label('Use warning 2').check()
                expect(page.locator('#warning-1')).to_be_enabled()
                page.locator('#warning-1').fill('60')
                page.locator('#alert-form [name=limit_usd]').fill('0.01')
                page.locator('#alert-form').get_by_role('button', name='Add alert').click()
                expect(page.get_by_role('dialog')).to_have_count(0)
                expect(page.locator('#alert-rows li')).to_have_count(1)
                expect(page.locator('#alert-rows')).to_contain_text('Daily limit for all projects')
                expect(page.locator('#alert-rows')).to_contain_text('warns at 60%, 80%')
                expect(page.locator('#alert-banner')).to_contain_text('Cost limit exceeded')
                page.locator('.nav', has_text='Sessions').click()
                expect(page.locator('#alert-banner')).to_be_visible()
                page.wait_for_function('window.__notes.length === 1')
                page.wait_for_timeout(2500)  # later polls must not notify again for the same window and level
                assert page.evaluate('window.__notes') == ['Muse Cost: limit exceeded']
                page.locator('.nav', has_text='Alerts').click()
                # Edit opens the same modal pre-filled; saving updates the alert in place.
                page.get_by_role('button', name='Edit').click()
                expect(page.get_by_role('dialog', name='Edit alert')).to_be_visible()
                expect(page.locator('#alert-form [name=limit_usd]')).to_have_value('0.01')
                expect(page.locator('#warning-0')).to_have_value('60')
                expect(page.get_by_label('Use warning 2')).to_be_checked()
                expect(page.get_by_label('Use warning 3')).not_to_be_checked()
                page.locator('#alert-form [name=period]').select_option('week')
                page.get_by_role('button', name='Save alert').click()
                expect(page.locator('#alert-rows')).to_contain_text('Weekly limit for all projects')
                expect(page.locator('#alert-rows li')).to_have_count(1)
                page.get_by_role('button', name='Pause').click()
                expect(page.locator('#alert-banner')).to_have_count(0, timeout=5000)
                # Preview shows a sample bar without saving anything; it can be ended at once.
                page.locator('#preview-warning').click()
                expect(page.locator('#alert-banner')).to_contain_text('Preview')
                expect(page.locator('#alert-banner')).to_contain_text('Approaching a cost limit')
                page.get_by_role('button', name='End preview').click()
                expect(page.locator('#alert-banner')).to_have_count(0)
                page.locator('#test-notification').click()
                assert page.evaluate('window.__notes')[-1] == 'Muse Cost: test notification'
                page.once('dialog', lambda dialog: dialog.accept())
                page.get_by_role('button', name='Delete').click()
                expect(page.locator('#alert-rows li')).to_have_count(0)
                # Project pages offer a pre-filled alert for that project.
                page.goto(base + '/projects/Research')
                page.get_by_role('link', name='Add alert for this project →').click()
                expect(page.get_by_role('dialog', name='Add alert')).to_be_visible()
                expect(page.locator('#alert-form [name=project]')).to_have_value('Research')
                page.get_by_role('button', name='Cancel').click()
                page.reload()
                expect(page.locator('#add-alert')).to_be_visible()
                expect(page.get_by_role('dialog')).to_have_count(0)  # the pre-filled dialog opens once, not on refresh
                # Pricing: a local rate overrides the public one and changes the estimate at once.
                page.locator('.nav', has_text='Pricing').click()
                row = page.locator('#pricing-rows tr', has_text='muse-spark-1.3')
                expect(row).to_contain_text('Public rate')
                row.get_by_role('button', name='Override').click()
                for name in ('input_per_million', 'cached_input_per_million', 'output_per_million'):
                    page.locator(f'.rate-form [name={name}]').fill('0')
                page.get_by_role('button', name='Save local rate').click()
                expect(row).to_contain_text('Local rate')
                expect(row).to_contain_text('$0.00')
                row.get_by_role('button', name='Remove').click()
                expect(row).to_contain_text('Public rate')
                page.locator('.nav', has_text='Overview').click()
                expect(page.locator('#input')).to_have_text('301,234')
                page.screenshot(path='/tmp/muse-cost-react-overview.png', full_page=True)
                page.set_viewport_size({'width': 390, 'height': 844})
                open_filters()
                expect(page.locator('[name=session]')).to_be_visible()
                page.locator('[name=project]').select_option('Research')
                expect(page.locator('#input')).to_have_text('90,000')
                page.locator('[name=project]').select_option('Website')
                expect(page.locator('#input')).to_have_text('151,234')
                page.screenshot(path='/tmp/muse-cost-filters-mobile.png', full_page=True)
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
                page.keyboard.press('Escape')
                page.locator('.nav', has_text='Breakdown').click()
                page.get_by_role('button', name='Token share').click()
                expect(page.get_by_role('tooltip')).to_be_visible()
                box = page.get_by_role('tooltip').bounding_box()
                assert box['x'] >= 0 and box['x'] + box['width'] <= 390
                page.goto(base + '/breakdown?period=all&project=Website&model=&from=&to=')
                expect(page).to_have_url(base + '/breakdown')
                check_filter('project', 'Website')
                expect(page.locator('#breakdown-rows tr')).to_have_count(3)
                page.goto(base + '/activity')
                expect(page).to_have_url(base + '/sessions')
                page.goto(base + '/sessions/no-such-session')
                expect(page.get_by_role('heading', name='Session not found')).to_be_visible()
                # A period with no usage costs $0.00, not "Unknown".
                page.goto(base + '/?period=all&from=2000-01-01&to=2000-01-02')
                expect(page.locator('#cost')).to_have_text('$0.00')
                expect(page.locator('#daily-cost')).to_have_text('$0.00')
                assert not errors, errors
                browser.close()
                print('React browser checks passed: clean URLs, client navigation, state persistence, filters, filter chips, session and project drill-down, subagent tree, cost chart, alerts, banner, notifications, local pricing, live updates, export, tooltips, history, mobile, legacy URLs.')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    main()
