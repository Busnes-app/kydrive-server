#!/usr/bin/env python3
"""Disposable real-image check. Never uses an existing Compose project or volumes."""
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.request
import uuid


def main():
    root = Path(__file__).resolve().parents[1]
    project = 'kydrive-office-test-' + uuid.uuid4().hex[:12]
    with tempfile.TemporaryDirectory(prefix='office-compose-') as scratch:
        key = Path(scratch) / 'jwt'
        value = secrets.token_hex(32)
        key.write_text(value)
        key.chmod(0o644)
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        env = dict(os.environ, KY_OFFICE_JWT_FILE=str(key), KY_OFFICE_HTTP_PORT=str(port))
        command = ['docker', 'compose', '-p', project, '-f', str(root / 'docker-compose.office.yml')]

        def compose(*args, check=True):
            return subprocess.run(command + list(args), env=env, capture_output=True, text=True, check=check)

        def docker(*args):
            return subprocess.check_output(['docker', *args], text=True).strip()

        def healthy():
            deadline = time.monotonic() + 300
            while time.monotonic() < deadline:
                try:
                    with urllib.request.urlopen(f'http://127.0.0.1:{port}/healthcheck', timeout=5) as response:
                        if response.status == 200 and response.read(16).strip() == b'true':
                            return
                except (OSError, TimeoutError):
                    pass
                time.sleep(2)
            raise RuntimeError('editor did not become healthy within five minutes')

        config = json.loads(compose('config', '--format', 'json').stdout)
        service = config['services']['euro-office']
        assert '@sha256:' in service['image']
        assert len(service['volumes']) == 5
        assert 'JWT_SECRET' not in service['environment']
        assert int(service['mem_limit']) == 4 * 1024**3 and service['cpus'] == 2
        try:
            refused = compose('run', '--rm', '--no-deps', 'euro-office', check=False)
            assert refused.returncode != 0 and 'mode 0600' in refused.stderr, 'shared JWT file was accepted'
            key.chmod(0o600)
            compose('up', '-d', 'euro-office')
            healthy()
            container = compose('ps', '-q', 'euro-office').stdout.strip()
            inspect = json.loads(docker('inspect', container))[0]
            assert value not in json.dumps(inspect['Config']['Env'])
            marker = '/var/lib/euro-office/.compose-persistence-check'
            docker('exec', container, 'touch', marker)
            compose('up', '-d', '--force-recreate', 'euro-office')
            healthy()
            replacement = compose('ps', '-q', 'euro-office').stdout.strip()
            assert replacement != container
            docker('exec', replacement, 'test', '-f', marker)
            print('PASS: private JWT refusal, editor health, no secret environment, persistent data after recreation')
        finally:
            result = compose('down', '--volumes', '--remove-orphans', check=False)
            if result.returncode:
                raise RuntimeError('owned editor fixture cleanup failed')


if __name__ == '__main__':
    main()
