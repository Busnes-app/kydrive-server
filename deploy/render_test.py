import argparse
import json
import re
from pathlib import Path
import tempfile
import subprocess
import unittest
from render import render, https_origin, pinned_image, EDITOR_IMAGE
class RenderTests(unittest.TestCase):
    def test_boundary_and_workload(self):
        for invalid in ['http://drive.test', 'https://user:pass@drive.test', 'https://drive.test/path', 'https://drive.test?token=x']:
            with self.assertRaises(argparse.ArgumentTypeError):https_origin(invalid)
        with self.assertRaises(argparse.ArgumentTypeError):pinned_image('editor:latest')
        with tempfile.TemporaryDirectory() as d:
            key=Path(d)/'jwt';key.write_text('synthetic-test-secret-at-least-32-bytes');key.chmod(0o600)
            a=argparse.Namespace(editor_secret_file=str(key),output=d+'/out',drive_url='https://drive.test',editor_url='https://editor.test',storage_class='local-path',editor_image=EDITOR_IMAGE,ingress_class='nginx',kubernetes_drive=False)
            render(a);items=json.loads((Path(a.output)/'stack.json').read_text())['items'];dep=next(x for x in items if x['kind']=='Deployment');self.assertEqual(dep['spec']['replicas'],1);self.assertEqual(dep['spec']['strategy']['type'],'Recreate');self.assertEqual(dep['spec']['template']['spec']['nodeSelector']['kubernetes.io/arch'],'amd64');self.assertEqual((Path(a.output)/'stack.json').stat().st_mode&0o777,0o600)
            pod=dep['spec']['template']['spec']
            init=pod['initContainers'][0]
            self.assertEqual(init['image'], EDITOR_IMAGE)
            self.assertIn('test -z', init['command'][2])
            self.assertIn('postgresql:/var/lib/postgresql', init['command'][2])
            self.assertIn('editor-runtime:/var/lib/euro-office', init['command'][2])
            self.assertEqual(pod['containers'][0]['volumeMounts'][0]['subPath'], 'editor-runtime')
            # Exercise first-start ownership/modes, including the mktemp root.
            script = init['command'][2]
            sources = [('postgresql','/var/lib/postgresql'), ('rabbitmq','/var/lib/rabbitmq'), ('redis','/var/lib/redis'), ('editor-runtime','/var/lib/euro-office'), ('editor-data','/var/www/euro-office/Data')]
            seed = Path(d)/'seed'; seed.mkdir()
            script = script.replace('/seed', str(seed))
            for name, source in sources:
                original = Path(d)/name; original.mkdir(mode=0o755)
                (original/'bundled').write_text(name)
                script = script.replace(source, str(original))
            subprocess.run(['/bin/sh','-ec',script],check=True)
            for name, _ in sources:
                self.assertEqual((seed/name).stat().st_mode&0o777,0o755)
                self.assertEqual((seed/name/'bundled').read_text(),name)
            subprocess.run(['/bin/sh','-ec',script],check=True)
            with self.assertRaises(ValueError):render(a)
            # INSTALL.txt names only the origins the operator supplied.
            notes=(Path(a.output)/'INSTALL.txt').read_text()
            self.assertIn('pair KyRecovery at your KyRecovery origin', notes)
            self.assertEqual(set(re.findall(r'https?://[^\s/]+', notes)), {'https://drive.test'})
    def test_seed_resumes_after_interruption(self):
        with tempfile.TemporaryDirectory() as d:
            key=Path(d)/'jwt';key.write_text('synthetic-test-secret-at-least-32-bytes');key.chmod(0o600)
            a=argparse.Namespace(editor_secret_file=str(key),output=d+'/out',drive_url='https://drive.test',editor_url='https://editor.test',storage_class='local-path',editor_image=EDITOR_IMAGE,ingress_class='nginx',kubernetes_drive=False)
            render(a);items=json.loads((Path(a.output)/'stack.json').read_text())['items']
            script=next(x for x in items if x['kind']=='Deployment')['spec']['template']['spec']['initContainers'][0]['command'][2]
            seed=Path(d)/'seed';seed.mkdir();script=script.replace('/seed',str(seed))
            for name, source in [('postgresql','/var/lib/postgresql'), ('rabbitmq','/var/lib/rabbitmq'), ('redis','/var/lib/redis'), ('editor-runtime','/var/lib/euro-office'), ('editor-data','/var/www/euro-office/Data')]:
                script=script.replace(source,str(Path(d)/name))
                if name!='rabbitmq': (Path(d)/name).mkdir(); (Path(d)/name/'bundled').write_text(name)
            # rabbitmq's source is missing: the start dies after postgresql moved, leaving a stage.
            self.assertNotEqual(subprocess.run(['/bin/sh','-ec',script]).returncode,0)
            self.assertTrue(any(seed.glob('.seed-rabbitmq.*')))
            (Path(d)/'rabbitmq').mkdir(); (Path(d)/'rabbitmq'/'bundled').write_text('rabbitmq')
            subprocess.run(['/bin/sh','-ec',script],check=True)
            self.assertEqual(list(seed.glob('.seed-*')),[])
            for name in ['postgresql','rabbitmq','redis','editor-runtime','editor-data']:
                self.assertEqual((seed/name/'bundled').read_text(),name)
            # A non-empty destination with no marker is still refused.
            (seed/'.vendor-state-seeded').unlink()
            for f in seed.glob('.seeded-*'): f.unlink()
            self.assertNotEqual(subprocess.run(['/bin/sh','-ec',script]).returncode,0)
    def test_drive_container_is_locked_down(self):
        with tempfile.TemporaryDirectory() as d:
            key=Path(d)/'jwt';key.write_text('synthetic-test-secret-at-least-32-bytes');key.chmod(0o600)
            a=argparse.Namespace(editor_secret_file=str(key),output=d+'/out',drive_url='https://drive.test',editor_url='https://editor.test',storage_class='local-path',editor_image=EDITOR_IMAGE,ingress_class='nginx',kubernetes_drive=True,drive_image='ghcr.io/busnes-app/kydrive-server@sha256:'+'a'*64,bulk_pvc='bulk')
            render(a);items=json.loads((Path(a.output)/'stack.json').read_text())['items']
            pods={x['metadata']['name']:x['spec']['template']['spec'] for x in items if x['kind']=='Deployment'}
            drive=pods['kydrive']['containers'][0]
            self.assertEqual(drive['securityContext'],{'allowPrivilegeEscalation':False,'capabilities':{'drop':['ALL']},'seccompProfile':{'type':'RuntimeDefault'},'readOnlyRootFilesystem':True})
            self.assertIn({'name':'tmp','mountPath':'/tmp'},drive['volumeMounts'])
            self.assertIn({'name':'tmp','emptyDir':{}},pods['kydrive']['volumes'])
            self.assertEqual({m['mountPath'] for m in drive['volumeMounts']},{'/app/data','/app/backups','/app/bulk','/tmp'})
            # Euro-Office runs as root by design and keeps its own defaults.
            self.assertNotIn('securityContext',pods['euro-office']['containers'][0])
            self.assertNotIn('securityContext',pods['euro-office'])
