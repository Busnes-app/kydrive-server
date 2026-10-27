"""Verify the frozen batch; regenerate only its inventory and duplicate audit."""
import collections
import csv
import hashlib
import json
from pathlib import Path
import struct
import subprocess

root = Path(__file__).resolve().parent
manifest = json.loads((root / 'manifest-00-before.json').read_text())
sha = lambda b: hashlib.sha256(b).hexdigest()
assert manifest['complete'] and len(manifest['files']) == 40
assert sha((root / 'capture.mjs').read_bytes()) == manifest['capture_driver_sha256']
expected = {(s, d, t) for s in manifest['surfaces'] for d in ['desktop', 'mobile'] for t in ['light', 'dark']}
assert expected == {(f['surface'], f['device'], f['theme']) for f in manifest['files']}
assert {p.relative_to(root).as_posix() for p in (root / 'screenshots').glob('*.png')} == {f['file'] for f in manifest['files']}
byte_groups, pixel_groups = collections.defaultdict(list), collections.defaultdict(list)
rows = []
for f in manifest['files']:
    path = root / f['file']
    data = path.read_bytes()
    width, height = struct.unpack('>II', data[16:24])
    assert data[:8] == b'\x89PNG\r\n\x1a\n'
    assert (width, height) == ((1280, 900) if f['device'] == 'desktop' else (390, 844))
    assert sha(data) == f['sha256'] and len(data) == f['bytes']
    assert f['dpr'] == 1 and f['saved_theme'] is None and f['overflow_px'] == 0
    assert f['computed_color_scheme'] == f['theme'] and f['service_worker_controller']
    pixel_hash = subprocess.check_output(['magick', 'identify', '-format', '%#', str(path)], text=True)
    byte_groups[f['sha256']].append(f['file'])
    pixel_groups[(width, height, pixel_hash)].append(f['file'])
    notes = 'Individually inspected; no capture defect observed.'
    if f['surface'] == 'login':
        notes += ' Empty fields display shipped placeholders; security check completed naturally.'
    if f['surface'] in ['new-folder', 'preview', 'share', 'versions']:
        notes += ' Background blur/dimming is the product dialog backdrop.'
    if f['device'] == 'mobile' and f['surface'] in ['files', 'folder', 'appearance', 'account']:
        notes += ' Viewport crop; more page content below fold.'
    if f['device'] == 'desktop' and f['surface'] == 'account':
        notes += ' Viewport crop; linked-computers section below fold.'
    if f['surface'] == 'share':
        notes += ' No people or links created; empty share form only.'
    if f['surface'] == 'versions':
        notes += ' One actual version; timestamps reflect fixture creation, not a frozen clock.'
    rows.append({k: f[k] for k in ['file', 'surface', 'device', 'theme', 'account', 'sha256', 'bytes', 'width', 'height']} | {'decoded_pixel_signature': pixel_hash, 'inspection': notes})
with (root / 'inventory-00-before.csv').open('w', newline='') as out:
    writer = csv.DictWriter(out, fieldnames=rows[0].keys())
    writer.writeheader()
    writer.writerows(rows)
audit = {'files': len(rows), 'unique_byte_hashes': len(byte_groups), 'byte_duplicate_groups': [v for v in byte_groups.values() if len(v) > 1], 'unique_decoded_pixel_signatures': len(pixel_groups), 'pixel_duplicate_groups': [v for v in pixel_groups.values() if len(v) > 1], 'method': 'SHA-256 of original PNG and ImageMagick decoded-pixel signature, grouped with dimensions; no perceptual-similarity claim', 'confirmation_recaptures': 0, 'superseded_artifacts': []}
(root / 'duplicate-audit-00-before.json').write_text(json.dumps(audit, indent=2) + '\n')
print(json.dumps(audit))
