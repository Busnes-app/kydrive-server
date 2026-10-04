#!/usr/bin/env python3
"""Render a reviewable deployment. Never invokes kubectl or deploys a NAS."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
from urllib.parse import urlsplit

EDITOR_IMAGE = 'ghcr.io/euro-office/documentserver@sha256:beca380debb9b4eadb7e4c662d112a325c5df0f2179453a4affd8e6ba5d00562'

def https_origin(value):
    u = urlsplit(value)
    if u.scheme != 'https' or not u.hostname or u.username or u.password or u.path not in ('', '/') or u.query or u.fragment:
        raise argparse.ArgumentTypeError('HTTPS origin required')
    return value.rstrip('/')

def name(value):
    if not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', value):
        raise argparse.ArgumentTypeError('Kubernetes DNS label required')
    return value

def pinned_image(value):
    if not re.fullmatch(r'[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}', value):
        raise argparse.ArgumentTypeError('Image must include a sha256 digest')
    return value

def resource(kind, name, spec=None):
    r = {'apiVersion': 'v1', 'kind': kind, 'metadata': {'name': name, 'namespace': 'kydrive'}}
    if spec is not None: r['spec'] = spec
    return r

def pvc(n, storage, size):
    return resource('PersistentVolumeClaim', n, {'accessModes': ['ReadWriteOnce'], 'storageClassName': storage, 'resources': {'requests': {'storage': size}}})

def deployment(n, image, env, mounts, volumes, port, root=False):
    pod = {'nodeSelector': {'kubernetes.io/arch': 'amd64'}, 'terminationGracePeriodSeconds': 1210, 'containers': [{'name': n, 'image': image, 'ports': [{'containerPort': port}], 'env': [{'name': k, 'value': v} for k,v in env.items()], 'volumeMounts': mounts, 'resources': {'requests': {'cpu': '500m', 'memory': '2Gi' if root else '256Mi'}, 'limits': {'memory': '4Gi' if root else '1Gi'}}, 'readinessProbe': {'httpGet': {'path': '/healthcheck' if root else '/healthz', 'port': port}, 'initialDelaySeconds': 90 if root else 5, 'periodSeconds': 10}, 'livenessProbe': {'httpGet': {'path': '/healthcheck' if root else '/healthz', 'port': port}, 'initialDelaySeconds': 180 if root else 30}}], 'volumes': volumes}
    if not root: pod['securityContext'] = {'runAsNonRoot': True, 'runAsUser': 1000, 'runAsGroup': 1000, 'fsGroup': 1000}
    d = resource('Deployment', n, {'replicas': 1, 'strategy': {'type': 'Recreate'}, 'selector': {'matchLabels': {'app': n}}, 'template': {'metadata': {'labels': {'app': n}}, 'spec': pod}})
    d['apiVersion'] = 'apps/v1'
    return d

def expose(n, origin, port, ingress_class):
    s = resource('Service', n, {'selector': {'app': n}, 'ports': [{'port': 80, 'targetPort': port}]})
    host = urlsplit(origin).hostname
    ing = resource('Ingress', n, {'ingressClassName': ingress_class, 'tls': [{'hosts': [host], 'secretName': n+'-tls'}], 'rules': [{'host': host, 'http': {'paths': [{'path': '/', 'pathType': 'Prefix', 'backend': {'service': {'name': n, 'port': {'number': 80}}}}]}}]})
    ing['apiVersion'] = 'networking.k8s.io/v1'
    ing['metadata']['annotations'] = {'nginx.ingress.kubernetes.io/proxy-read-timeout': '3600', 'nginx.ingress.kubernetes.io/proxy-send-timeout': '3600', 'nginx.ingress.kubernetes.io/proxy-body-size': '129m'}
    return [s, ing]

def render(a):
    secret_path = Path(a.editor_secret_file)
    if secret_path.stat().st_mode & 0o077: raise ValueError('Editor secret file must have mode 0600')
    jwt = secret_path.read_text().strip()
    if len(jwt.encode()) < 32: raise ValueError('Editor JWT secret must contain at least 32 bytes')
    output = Path(a.output)
    if output.exists() and any(output.iterdir()): raise ValueError('Output directory must be empty')
    output.mkdir(mode=0o700, parents=True, exist_ok=True); output.chmod(0o700)
    ns = {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'kydrive'}}
    sec = resource('Secret', 'editor-jwt'); sec['type'] = 'Opaque'; sec['data'] = {'secret': base64.b64encode(jwt.encode()).decode()}
    claim = pvc('euro-runtime', a.storage_class, '30Gi')
    env = {'JWT_ENABLED': 'true', 'ALLOW_PRIVATE_IP_ADDRESS': 'false', 'ALLOW_META_IP_ADDRESS': 'false'}
    euro = deployment('euro-office', a.editor_image, env, [{'name': 'runtime', 'mountPath': '/var/lib/euro-office', 'subPath': 'editor-runtime'}], [{'name': 'runtime', 'persistentVolumeClaim': {'claimName': 'euro-runtime'}}], 80, True)
    euro['spec']['template']['spec']['containers'][0]['env'].append({'name': 'JWT_SECRET', 'valueFrom': {'secretKeyRef': {'name': 'editor-jwt', 'key': 'secret'}}})
    # The bundled distribution uses PostgreSQL, RabbitMQ and Redis internally. Persist its
    # database and document cache independently of the image, with no extra replicas.
    pod = euro['spec']['template']['spec']
    pod['containers'][0]['volumeMounts'].append({'name': 'runtime', 'mountPath': '/var/www/euro-office/Data', 'subPath': 'editor-data'})
    for directory in ['postgresql', 'rabbitmq', 'redis']:
        pod['containers'][0]['volumeMounts'].append({'name': 'runtime', 'mountPath': '/var/lib/'+directory, 'subPath': directory})
    # Preserve the distribution's initialized databases before empty PVC mounts hide them.
    seed = 'set -eu\nif test ! -f /seed/.vendor-state-seeded; then\n  for pair in postgresql:/var/lib/postgresql rabbitmq:/var/lib/rabbitmq redis:/var/lib/redis editor-runtime:/var/lib/euro-office editor-data:/var/www/euro-office/Data; do\n    name=${pair%%:*}; source=${pair#*:}\n    mkdir -p /seed/$name\n    test -z "$(ls -A /seed/$name)"\n    stage=$(mktemp -d /seed/.seed-$name.XXXXXX)\n    cp -a "$source/." "$stage/"\n    chown --reference="$source" "$stage"\n    chmod --reference="$source" "$stage"\n    rmdir /seed/$name\n    mv "$stage" /seed/$name\n  done\n  touch /seed/.vendor-state-seeded\nfi\n'
    pod['initContainers'] = [{'name': 'seed-bundled-services', 'image': a.editor_image, 'command': ['/bin/sh', '-ec', seed], 'volumeMounts': [{'name': 'runtime', 'mountPath': '/seed'}]}]
    items = [ns, sec, claim, euro] + expose('euro-office', a.editor_url, 80, a.ingress_class)
    profile = 'NAS endpoint '+a.drive_url
    if a.kubernetes_drive:
        if not a.drive_image or not a.bulk_pvc: raise ValueError('Kubernetes drive requires --drive-image and --bulk-pvc')
        items += [pvc('drive-data', a.storage_class, '100Gi'), pvc('drive-capsules', a.storage_class, '10Gi')]
        env = {'KY_APP_NAME': 'KyDrive', 'KY_ENV': 'production', 'KY_HOST': '0.0.0.0', 'KY_PORT': '8080', 'KY_APP_URL': a.drive_url, 'KY_DATA_DIR': '/app/data', 'KY_DB_DRIVER': 'sqlite', 'KY_BACKUP_DIR': '/app/backups', 'KYDRIVE_BULK_BACKUP_REPOSITORY': '/app/bulk', 'KYDRIVE_EDITOR_URL': a.editor_url, 'KYDRIVE_EDITOR_DRIVE_URL': a.drive_url, 'KY_SSO_AUTO_PROVISION': 'false'}
        volumes = [{'name': n, 'persistentVolumeClaim': {'claimName': c}} for n,c in [('data','drive-data'),('capsules','drive-capsules'),('bulk',a.bulk_pvc)]]
        mounts = [{'name': n, 'mountPath': p} for n,p in [('data','/app/data'),('capsules','/app/backups'),('bulk','/app/bulk')]]
        d = deployment('kydrive', a.drive_image, env, mounts, volumes, 8080)
        container = d['spec']['template']['spec']['containers'][0]
        container['envFrom'] = [{'secretRef': {'name': 'drive-integrations'}}]
        container['env'].append({'name': 'KYDRIVE_EDITOR_SECRET', 'valueFrom': {'secretKeyRef': {'name': 'editor-jwt', 'key': 'secret'}}})
        items += [d] + expose('kydrive', a.drive_url, 8080, a.ingress_class)
        profile = 'Kubernetes single-instance drive (requires precreated drive-integrations Secret and independent bulk PVC)'
    package = {'apiVersion': 'v1', 'kind': 'List', 'items': items}
    manifest = output/'stack.json'; manifest.write_text(json.dumps(package, indent=2)+'\n'); manifest.chmod(0o600)
    notes = output/'INSTALL.txt'; notes.write_text(f'Profile: {profile}\nEditor image: {a.editor_image}\nRendered only; not installed or ready.\nSupply TLS Secrets euro-office-tls and (if applicable) kydrive-tls using existing certificate tooling.\nReview storage ownership, HTTPS reachability, DNS, JWT and identity assignment before kubectl apply -f stack.json.\nIf drive resolves to private addresses, keep private access off until an egress policy or proxy constrains it to the drive endpoint; document the explicit exception before enabling it.\nInitialize the independent Restic repository with kydrive-server bulk-init.\nThen pair KyRecovery at https://kyrecovery.urlxl.us/ or pin the ceremony public key manually.\nRead README.md and docs/ACCEPTANCE.md; readiness probes alone do not establish integration readiness.\n')
    print(str(manifest))

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--drive-url',required=True,type=https_origin);p.add_argument('--editor-url',required=True,type=https_origin)
    p.add_argument('--editor-secret-file',required=True);p.add_argument('--output',required=True)
    p.add_argument('--editor-image',default=EDITOR_IMAGE,type=pinned_image);p.add_argument('--drive-image',type=pinned_image)
    p.add_argument('--storage-class',required=True,type=name);p.add_argument('--ingress-class',default='nginx',type=name)
    p.add_argument('--kubernetes-drive',action='store_true');p.add_argument('--bulk-pvc',type=name)
    a=p.parse_args()
    try:render(a)
    except (ValueError,OSError) as e:p.error(str(e))
if __name__=='__main__':main()
