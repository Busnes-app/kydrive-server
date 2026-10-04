import argparse
import json
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
