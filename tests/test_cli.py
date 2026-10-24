import json
import os
import select
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from test_core import event

ROOT=Path(__file__).resolve().parents[1]


class CLITests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        self.root=Path(self.tmp.name)
        self.source=self.root/'session.jsonl'
        self.source.write_text(json.dumps(event())+'\n')
        self.base=[sys.executable,str(ROOT/'muse-cost')]
        self.options=['--db',str(self.root/'db.sqlite3'),'--source',str(self.source)]
    def tearDown(self):self.tmp.cleanup()
    def command(self,*args):
        return subprocess.run(self.base+list(args)+self.options,text=True,capture_output=True,check=True).stdout
    def test_cli_export_and_manual_observation(self):
        rows=json.loads(self.command('export','--period','all'))
        self.assertEqual(len(rows),1)
        self.assertNotIn('source',rows[0])
        self.assertIsNone(rows[0]['estimated_api_equivalent_usd'])
        self.assertEqual(len(json.loads(self.command('export','--period','all'))),1)
        added=json.loads(self.command('reconcile','add','--amount','12.34','--currency','CAD'))
        self.assertEqual(added[0]['amount'],'12.34')
        self.assertEqual(added[0]['currency'],'CAD')
        self.assertIn('unknown',self.command('tui','--once','--period','all'))
    def test_warning_levels_and_invalid_filters(self):
        text=self.command('alert','set','--monthly-usd','30','--at','50,80,100')
        self.assertIn('not a spend cap',text)
        self.command('alert','set','--monthly-usd','0.00001')
        prices=self.root/'prices.json'
        prices.write_text(json.dumps({'rates':[{'model':'test-model','currency':'USD','effective_from':'2020-01-01',
            'retrieved_at':'2020-01-01','snapshot_version':'fixture','source_url':'https://example.invalid',
            'input_per_million':'1','cached_input_per_million':'0.1','output_per_million':'2'}]}))
        self.assertIn('WARNING: Monthly limit for all projects',self.command('month','--prices',str(prices)))
        result=subprocess.run(self.base+['show','--from','bad-date']+self.options,
                              text=True,capture_output=True)
        self.assertEqual(result.returncode,1)
        self.assertNotIn('Traceback',result.stderr)

    def test_terminal_refresh_filters_and_exit(self):
        import pty
        import fcntl
        import struct
        import termios
        master,slave=pty.openpty()
        fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,150,0,0))
        process=subprocess.Popen(self.base+['tui','--interval','0.1','--period','all']+self.options,
                                 stdin=slave,stdout=slave,stderr=slave,env=dict(os.environ,TERM='xterm'))
        os.close(slave)
        output=b''
        def read_for(seconds):
            nonlocal output
            end=time.monotonic()+seconds
            while time.monotonic()<end:
                if select.select([master],[],[],0.05)[0]:
                    try:output+=os.read(master,65536)
                    except OSError:break
        try:
            read_for(0.4)
            with self.source.open('a') as f:f.write(json.dumps(event('live-2',inp=500))+'\n')
            os.write(master,b'r')
            read_for(0.4)
            os.write(master,b'g')
            read_for(0.2)
            os.write(master,b'f')
            read_for(0.1)
            self.assertIn(b'Period (today/week/month/all)',output)
            # Enter retains period; blank dates/project/model; choose parent session.
            os.write(master,b'\n\n\n\n\nparent\n\n')
            read_for(0.3)
            os.write(master,b'q')
            process.wait(timeout=3)
            self.assertEqual(process.returncode,0,output.decode(errors='replace'))
            self.assertIn(b'MUSE COST',output)
            self.assertIn(b'parent',output)
            rows=json.loads(self.command('export','--period','all'))
            self.assertEqual(sum(r['input'] for r in rows),600)
        finally:
            if process.poll() is None:process.kill();process.wait()
            os.close(master)

if __name__=='__main__':unittest.main()
