import json
import errno
import io
from contextlib import redirect_stdout
import sys
import threading
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from unittest.mock import patch

import test_core
from test_core import event
from meta_cost_center import cli
from meta_cost_center.webapp import DashboardServer, run


class WebTests(unittest.TestCase):
    write = test_core.CollectorTests.write
    def setUp(self):
        test_core.CollectorTests.setUp(self)
        self.write(event())
        self.server=DashboardServer(('127.0.0.1',0),str(self.root/'db.sqlite3'),[self.logs],{},self.rates,{'period':'all'},0.1)
        self.thread=threading.Thread(target=self.server.serve_forever,daemon=True)
        self.thread.start()
        self.url=f'http://127.0.0.1:{self.server.server_port}'
        for _ in range(100):
            with self.server.state_lock:
                ready=self.server.state['last_success'] is not None
            if ready:break
            time.sleep(.02)
        self.assertTrue(ready)
    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        test_core.CollectorTests.tearDown(self)
    def get(self,path,**kwargs):
        with urllib.request.urlopen(urllib.request.Request(self.url+path,**kwargs),timeout=5) as response:
            return response.status,response.read(),response.headers
    def test_react_routes_and_bootstrap(self):
        shell=self.get('/')[1]
        self.assertIn(b'id="root"',shell)
        for path in ('/breakdown','/activity','/sessions','/sessions/parent','/projects','/projects/my%20project'):
            self.assertEqual(self.get(path)[1],shell)
        for path in ('/breakdown/x','/sessions/a/b','/nope'):
            with self.assertRaises(urllib.error.HTTPError) as error:self.get(path)
            self.assertEqual(error.exception.code,404)
            error.exception.close()
        config=json.loads(self.get('/api/config')[1])
        self.assertEqual(config['filters'],{'period':'all'})
        self.assertEqual(config['instance_id'],self.server.instance_id)
        self.assertEqual(self.get('/app.js')[0],200)
        status,_,headers=self.get('/logo.png')
        self.assertEqual((status,headers['Content-Type']),(200,'image/png'))

    def test_explicit_filters_can_clear_launch_defaults(self):
        self.server.filters={'period':'all','project':'missing'}
        self.assertEqual(json.loads(self.get('/api/dashboard')[1])['totals']['events'],0)
        data=json.loads(self.get('/api/dashboard?period=all')[1])
        self.assertEqual(data['totals']['events'],1)
        self.assertIsNone(data['filters']['project'])

    def test_web_filters_refresh_and_exports(self):
        status,body,headers=self.get('/')
        self.assertEqual(status,200)
        self.assertIn(b'Usage overview',body)
        self.assertIn("default-src 'self'",headers['Content-Security-Policy'])
        data=json.loads(self.get('/api/dashboard')[1])
        self.assertEqual(data['totals']['input'],100)
        filtered=json.loads(self.get('/api/dashboard?model=missing')[1])
        self.assertEqual(filtered['totals']['events'],0)
        self.write(event('appended',inp=50))
        self.get('/api/refresh',method='POST')
        for _ in range(100):
            data=json.loads(self.get('/api/dashboard?session=parent')[1])
            if data['totals']['input']==150:break
            time.sleep(.02)
        self.assertEqual(data['totals']['input'],150)
        self.assertEqual(data['filters']['session'],'parent')
        exported=json.loads(self.get('/api/export')[1])
        self.assertNotIn('source',exported[0])
        self.assertNotIn(b'SECRET_',self.get('/api/dashboard')[1])
        self.assertIn(b'input',self.get('/api/export?format=csv')[1])
    def test_drill_down_endpoints(self):
        self.write(event('child-event','child',50),self.logs/'parent/subagent/child/session.jsonl')
        self.get('/api/refresh',method='POST')
        for _ in range(100):
            listed=json.loads(self.get('/api/sessions?period=all')[1])
            if listed and listed[0]['input']==150:break
            time.sleep(.02)
        self.assertEqual([(s['id'],s['input'],s['subagents']) for s in listed],[('parent',150,1)])
        detail=json.loads(self.get('/api/sessions/parent?period=all')[1])
        self.assertEqual(detail['tree']['children'][0]['id'],'child')
        self.assertNotIn(b'SECRET_',self.get('/api/sessions/parent?period=all')[1])
        projects=json.loads(self.get('/api/projects?period=all')[1])
        name=projects[0]['label']
        project=json.loads(self.get('/api/projects/'+urllib.parse.quote(name)+'?period=all')[1])
        self.assertEqual(project['totals']['input'],150)
        dashboard=json.loads(self.get('/api/dashboard?period=all')[1])
        self.assertEqual((dashboard['session_count'],dashboard['top_sessions'][0]['id']),(1,'parent'))
        for path,code in (('/api/sessions/missing',404),('/api/projects/missing',404),
                          ('/api/sessions?period=bad',400),('/api/sessions/parent?from=bad',400),('/api/dashboard/x',404)):
            with self.assertRaises(urllib.error.HTTPError) as error:self.get(path)
            self.assertEqual(error.exception.code,code,path)
            error.exception.close()
    def send_json(self,method,path,body,content_type='application/json',headers=None):
        request=urllib.request.Request(self.url+path,data=json.dumps(body).encode(),method=method,
                                       headers={'Content-Type':content_type,**(headers or {})})
        try:
            with urllib.request.urlopen(request,timeout=5) as response:return response.status,json.loads(response.read())
        except urllib.error.HTTPError as error:
            with error:return error.code,json.loads(error.read())
    def test_pricing_and_alert_api(self):
        self.assertEqual(self.get('/pricing')[0],200)
        self.assertEqual(self.get('/alerts')[0],200)
        models=json.loads(self.get('/api/pricing')[1])
        self.assertEqual([(m['model'],m['source']) for m in models],[('test-model','public')])
        self.assertEqual(self.send_json('PUT','/api/pricing/test-model',{'input_per_million':'0','cached_input_per_million':'0','output_per_million':'0'})[0],200)
        self.assertEqual(json.loads(self.get('/api/pricing')[1])[0]['source'],'local')
        self.assertEqual(json.loads(self.get('/api/dashboard?period=all')[1])['totals']['unknown_mode_equivalent_usd'],'0')
        self.assertEqual(self.send_json('DELETE','/api/pricing/test-model',{})[0],200)
        self.assertEqual(self.send_json('DELETE','/api/pricing/test-model',{})[0],404)
        # A high local rate makes the fixture's few tokens pass a one-cent limit.
        self.assertEqual(self.send_json('PUT','/api/pricing/test-model',{'input_per_million':'100000','cached_input_per_million':'10000','output_per_million':'200000'})[0],200)
        self.assertEqual(self.send_json('POST','/api/alerts',{'period':'day','limit_usd':'0.001'})[0],400)
        code,created=self.send_json('POST','/api/alerts',{'period':'day','limit_usd':'0.01'})
        self.assertEqual(code,201)
        dashboard=json.loads(self.get('/api/dashboard?period=all')[1])
        self.assertEqual([(a['id'],a['level']) for a in dashboard['alerts']],[(created['id'],'exceeded')])
        self.assertEqual(self.send_json('PUT',f'/api/alerts/{created["id"]}',{'period':'day','limit_usd':'5','enabled':False})[0],200)
        self.assertEqual(json.loads(self.get('/api/alerts')[1])[0]['level'],'paused')
        for method,path,body,expected in (('POST','/api/alerts',{'period':'day','limit_usd':'-1'},400),
                                          ('PUT','/api/alerts/999',{'period':'day','limit_usd':'1'},404),
                                          ('PUT','/api/alerts/abc',{'period':'day','limit_usd':'1'},404),
                                          ('POST','/api/pricing',{},404)):
            self.assertEqual(self.send_json(method,path,body)[0],expected,(method,path))
        # Cross-site writes: a form-encoded body or a foreign Origin is refused.
        self.assertEqual(self.send_json('POST','/api/alerts',{'period':'day','limit_usd':'1'},'application/x-www-form-urlencoded')[0],415)
        self.assertEqual(self.send_json('POST','/api/alerts',{'period':'day','limit_usd':'1'},headers={'Origin':'https://evil.example'})[0],403)
        self.assertEqual(self.send_json('DELETE',f'/api/alerts/{created["id"]}',{})[0],200)
        self.assertEqual(json.loads(self.get('/api/alerts')[1]),[])
    def test_occupied_port_preserves_original_error(self):
        with self.assertRaises(OSError) as error:
            DashboardServer(('127.0.0.1',self.server.server_port),self.root/'db.sqlite3',
                            [self.logs],{},self.rates,{'period':'all'},.1)
        self.assertEqual(error.exception.errno,errno.EADDRINUSE)

    def test_show_falls_back_to_a_free_port(self):
        occupied=self.server.server_port
        output=io.StringIO()
        with patch.object(DashboardServer,'serve_forever') as serve, redirect_stdout(output):
            run(self.root/'fallback.sqlite3',[self.logs],{}, {'period':'all'},self.rates,
                interval=.1,port=occupied,browser=False)
        serve.assert_called_once()
        self.assertIn(f'Port {occupied} is in use',output.getvalue())
        url=next(line.split(': ',1)[1] for line in output.getvalue().splitlines()
                 if line.startswith('Muse Cost dashboard:'))
        from urllib.parse import urlsplit
        chosen=urlsplit(url).port
        self.assertNotEqual(chosen,occupied)
        self.assertGreater(chosen,0)
        self.assertEqual(urlsplit(url).query,'')
        # Failed initialization must leave the existing service untouched.
        self.assertEqual(self.get('/')[0],200)

    def test_invalid_filters_and_foreign_requests(self):
        for path in ('/api/dashboard?period=bad','/api/dashboard?from=bad','/api/dashboard?billing_mode=bad'):
            with self.assertRaises(urllib.error.HTTPError) as error:self.get(path)
            self.assertEqual(error.exception.code,400)
            error.exception.close()
        for headers in ({'Host':'evil.example'},{'Origin':'https://evil.example'}):
            with self.assertRaises(urllib.error.HTTPError) as error:self.get('/api/dashboard',headers=headers)
            self.assertEqual(error.exception.code,403)
            error.exception.close()


class ShowCommandTests(unittest.TestCase):
    def test_show_dispatches_to_web_without_a_terminal(self):
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(sys,'argv',['muse-cost','show','--db',str(Path(tmp)/'db.sqlite3')]), patch('meta_cost_center.webapp.run') as run:
                cli.main()
            run.assert_called_once()
            self.assertTrue(run.call_args.args[-1])  # opens browser by default

if __name__=='__main__':unittest.main()
