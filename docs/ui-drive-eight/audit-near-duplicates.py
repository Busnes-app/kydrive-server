"""Reproduce ky-suite-v2 near-duplicate checks without recapturing originals."""
from pathlib import Path
import hashlib
import itertools
import json
import numpy as np
from PIL import Image

root = Path(__file__).resolve().parent
manifest = json.loads((root / 'manifest-00-before.json').read_text())
images = {}
for row in manifest['files']:
    data = (root / row['file']).read_bytes()
    assert hashlib.sha256(data).hexdigest() == row['sha256'], row['file']
    images[row['file']] = np.asarray(Image.open(root / row['file']).convert('RGBA'), dtype=np.int16)
comparisons = []
# ponytail: exhaustive pairs for this small frozen batch; group by size if batches grow.
for (left, a), (right, b) in itertools.combinations(images.items(), 2):
    if a.shape != b.shape:
        continue
    delta = np.abs(a - b)
    fraction = float(np.any(delta, axis=2).mean())
    maximum = int(delta.max())
    comparisons.append(dict(left=left, right=right, changed_fraction=fraction,
                            max_channel_delta=maximum,
                            near_duplicate=fraction < .001 and maximum <= 8))
assert len(images) == 40 and len(comparisons) == 380
audit = dict(method='RGBA; changed_fraction<0.001 AND max_channel_delta<=8',
             originals=len(images), comparisons=comparisons,
             near_duplicates=[row for row in comparisons if row['near_duplicate']])
(root / 'near-duplicate-audit-v1.json').write_text(json.dumps(audit, indent=2) + '\n')
print(f"Verified40 originals;380 same-size comparisons;{len(audit['near_duplicates'])} near duplicates")
