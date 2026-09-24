#!/usr/bin/env python3
"""Resumeable upload of the image gallery to the private R2 bucket."""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import mimetypes
from pathlib import Path
import sqlite3
import threading

ROOT = Path(__file__).resolve().parents[1]
APP = Path(__file__).resolve().parent
ACCOUNT = '645fe29b30ebce3e940e06365ce74872'
BUCKET = 'osh-prompt'


def objects(index, start, limit):
    with sqlite3.connect(f'file:{index}?mode=ro', uri=True) as db:
        db.row_factory = sqlite3.Row
        sql = "select id,source_code,path,thumb_path,sha256,bytes from images order by case source_code when 'tdeh' then 0 when 'qwen' then 1 else 2 end,translated desc,id limit ? offset ?"
        for row in db.execute(sql, (limit, start)):
            row = dict(row)
            for kind in ('thumb', 'original'):
                rel = row['thumb_path'] if kind == 'thumb' else row['path']
                ext = '.webp' if kind == 'thumb' else Path(rel).suffix.lower()
                key = f"v1/images/{row['source_code']}/{row['id']}/{kind}{ext}"
                path = ROOT / rel if rel else None
                if rel and (Path(rel).is_absolute() or '..' in Path(rel).parts):
                    raise ValueError('unsafe image path: ' + rel)
                if kind == 'thumb' and not path:
                    blob = db.execute('select webp from embedded_thumbs where id=?', (row['id'],)).fetchone()
                    if blob is None:
                        raise FileNotFoundError('embedded thumbnail: ' + row['id'])
                    yield row['id'], kind, key, None, blob[0], len(blob[0]), ''
                else:
                    size = row['bytes'] if kind == 'original' else path.stat().st_size
                    yield row['id'], kind, key, path, None, size, row['sha256'] if kind == 'original' else ''


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--credentials-file', type=Path)
    p.add_argument('--workers', type=int, default=8)
    p.add_argument('--start', type=int, default=0)
    p.add_argument('--limit', type=int, default=109661)
    p.add_argument('--plan', action='store_true')
    args = p.parse_args()
    index = APP / 'images_catalog.sqlite3'
    work = list(objects(index, args.start, args.limit))
    print(json.dumps({'objects': len(work), 'bytes': sum(x[5] for x in work), 'start': args.start, 'limit': args.limit}), flush=True)
    if args.plan:
        return
    if not args.credentials_file:
        raise SystemExit('--credentials-file required')
    creds = json.loads(args.credentials_file.read_text())
    import boto3
    from botocore.config import Config
    client = boto3.client('s3', endpoint_url=f'https://{ACCOUNT}.r2.cloudflarestorage.com',
        aws_access_key_id=creds['access_key_id'], aws_secret_access_key=creds['secret_access_key'],
        region_name='auto', config=Config(retries={'max_attempts': 8, 'mode': 'adaptive'}, max_pool_connections=args.workers+2))
    state_path = APP / 'images_upload_state.sqlite3'
    state = sqlite3.connect(state_path)
    state.execute('create table if not exists uploaded (key text primary key, bytes integer not null, completed_at text not null default current_timestamp)')
    completed = {key for (key,) in state.execute('select key from uploaded')}
    pending = [item for item in work if item[2] not in completed]
    print(json.dumps({'already_done': len(work)-len(pending), 'pending': len(pending)}), flush=True)
    lock = threading.Lock()
    count = {'done': 0, 'failed': 0, 'bytes': 0}

    def upload(item):
        _, kind, key, path, blob, size, sha = item
        extra = {'ContentType': 'image/webp' if kind == 'thumb' else (mimetypes.guess_type(path.name)[0] or 'application/octet-stream'),
                 'CacheControl': 'private, max-age=31536000, immutable'}
        if sha:
            extra['Metadata'] = {'sha256': sha}
        if path:
            with path.open('rb') as stream:
                client.put_object(Bucket=BUCKET, Key=key, Body=stream, ContentLength=size, **extra)
        else:
            client.put_object(Bucket=BUCKET, Key=key, Body=blob, ContentLength=size, **extra)
        remote = client.head_object(Bucket=BUCKET, Key=key)
        if remote['ContentLength'] != size or (sha and remote.get('Metadata', {}).get('sha256') != sha):
            raise RuntimeError('R2 verification failed: ' + key)
        return key, size

    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = [pool.submit(upload, item) for item in pending]
        for future in as_completed(futures):
            try:
                key, size = future.result()
                with lock:
                    state.execute('insert or replace into uploaded(key,bytes) values (?,?)', (key,size))
                    count['done'] += 1
                    count['bytes'] += size
                    if count['done'] % 100 == 0:
                        state.commit()
                        print(json.dumps(count), flush=True)
            except Exception as error:
                count['failed'] += 1
                print('ERROR', str(error)[:240], flush=True)
    state.commit()
    print(json.dumps(count), flush=True)
    if count['failed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
