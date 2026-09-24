#!/usr/bin/env python3
"""Upload accepted case assets to a private R2 bucket; safe to resume."""
import argparse
import concurrent.futures
import hashlib
import json
import mimetypes
import os
from pathlib import Path
import re
import sys
import threading

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = ROOT / '00_续作与清单' / 'manifest.json'
ACCOUNT = '645fe29b30ebce3e940e06365ce74872'
BUCKET = 'osh-prompt'
ID_PATTERN = re.compile(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,119}\Z')


def assets():
    records = json.loads(MANIFEST.read_text())['cases']
    for case in records:
        if case.get('review_status') != 'accepted' or not case.get('output_video'):
            continue
        case_id = case['id']
        if not ID_PATTERN.fullmatch(case_id) or '..' in case_id:
            raise ValueError(f'unsafe case id: {case_id!r}')
        entries = [('video', case['output_video'], case.get('sha256'))]
        if case.get('poster'):
            entries.append(('poster', case['poster'], None))
        for i, path in enumerate(case.get('input_images') or [], 1):
            entries.append((f'inputs/image-{i:02d}', path, None))
        for kind, relative, known_hash in entries:
            path = (ROOT / relative).resolve()
            if not path.is_relative_to(ROOT) or not path.is_file():
                raise FileNotFoundError(f'{case_id}: {relative}')
            key = f'v1/cases/{case_id}/{kind}{path.suffix.lower()}'
            yield {'id': case_id, 'kind': kind, 'path': path, 'key': key,
                   'size': path.stat().st_size, 'sha256': known_hash}


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--plan', action='store_true')
    parser.add_argument('--workers', type=int, default=6)
    parser.add_argument('--credentials-file', type=Path)
    args = parser.parse_args()
    work = list(assets())
    keys = [item['key'] for item in work]
    if len(keys) != len(set(keys)):
        raise ValueError('duplicate R2 keys')
    print(f'assets={len(work)} bytes={sum(x["size"] for x in work)} cases={len({x["id"] for x in work})}', flush=True)
    if args.plan:
        print('layout: v1/cases/<case-id>/video.<ext>, poster.<ext>, inputs/image-NN.<ext>')
        return
    import boto3
    from botocore.config import Config
    creds = {}
    if args.credentials_file:
        creds = json.loads(args.credentials_file.read_text())
    access = creds.get('access_key_id') or os.environ.get('R2_ACCESS_KEY_ID')
    secret = creds.get('secret_access_key') or os.environ.get('R2_SECRET_ACCESS_KEY')
    if not access or not secret:
        raise SystemExit('R2 credentials missing')
    client = boto3.client('s3', endpoint_url=f'https://{ACCOUNT}.r2.cloudflarestorage.com',
        aws_access_key_id=access, aws_secret_access_key=secret, region_name='auto',
        config=Config(retries={'max_attempts': 8, 'mode': 'adaptive'}, max_pool_connections=args.workers + 2))
    lock = threading.Lock()
    done = {'uploaded': 0, 'skipped': 0, 'failed': 0, 'bytes': 0}

    def upload(item):
        sha = item['sha256'] or digest(item['path'])
        try:
            current = client.head_object(Bucket=BUCKET, Key=item['key'])
            if current['ContentLength'] == item['size'] and current.get('Metadata', {}).get('sha256') == sha:
                return ('skipped', item)
        except client.exceptions.ClientError as error:
            if error.response.get('ResponseMetadata', {}).get('HTTPStatusCode') != 404:
                raise
        mime = mimetypes.guess_type(item['path'].name)[0] or 'application/octet-stream'
        with item['path'].open('rb') as stream:
            client.put_object(Bucket=BUCKET, Key=item['key'], Body=stream,
                ContentLength=item['size'], ContentType=mime,
                CacheControl='public, max-age=31536000, immutable', Metadata={'sha256': sha})
        current = client.head_object(Bucket=BUCKET, Key=item['key'])
        if current['ContentLength'] != item['size'] or current.get('Metadata', {}).get('sha256') != sha:
            raise RuntimeError('remote verification failed: ' + item['key'])
        return ('uploaded', item)

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = [pool.submit(upload, item) for item in work]
        for future in concurrent.futures.as_completed(futures):
            try:
                status, item = future.result()
            except Exception as error:
                with lock:
                    done['failed'] += 1
                    print(f'ERROR {error}', file=sys.stderr, flush=True)
                continue
            with lock:
                done[status] += 1
                done['bytes'] += item['size']
                if (done['uploaded'] + done['skipped']) % 100 == 0:
                    print(json.dumps(done), flush=True)
    print(json.dumps(done), flush=True)
    if done['failed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
