"""Create the server catalog using only images verified in R2."""
from pathlib import Path
import sqlite3
import shutil

APP = Path(__file__).resolve().parent
STATE = APP / 'images_upload_state.sqlite3'
SOURCE = APP / 'images_catalog.sqlite3'
OUTPUT = Path('/tmp/prompt-osh-private/images_catalog_published.sqlite3')


def main():
    with sqlite3.connect(f'file:{STATE}?mode=ro', uri=True) as state:
        flags = {}
        for (key,) in state.execute('select key from uploaded'):
            parts = key.split('/')
            if len(parts) != 5 or parts[0:2] != ['v1', 'images']:
                continue
            kind = parts[4].split('.')[0]
            if kind in ('thumb', 'original'):
                flags[parts[3]] = flags.get(parts[3], 0) | (1 if kind == 'thumb' else 2)
    ready = [image_id for image_id, flag in flags.items() if flag == 3]
    temp = Path('/tmp/prompt-osh-images-published.sqlite3')
    temp.unlink(missing_ok=True)
    with sqlite3.connect(temp, uri=True) as db:
        db.execute('attach database ? as source', (f'file:{SOURCE}?mode=ro',))
        db.execute('create table images as select * from source.images where 0')
        db.execute('create temporary table ready(id text primary key)')
        db.executemany('insert into ready values (?)', ((x,) for x in ready))
        db.execute('insert into images select i.* from source.images i join ready r on i.id=r.id')
        db.execute('create unique index images_id on images(id)')
        db.execute('create index images_source_order on images(source_code,translated desc,image_name)')
        db.execute('create index images_order on images(translated desc,source_code,image_name)')
        db.execute('create table embedded_thumbs (id text primary key, webp blob not null)')
        db.execute('analyze main')
    staged = OUTPUT.with_suffix('.new')
    shutil.copy2(temp, staged)
    staged.replace(OUTPUT)
    print('published_images', len(ready), 'database_bytes', OUTPUT.stat().st_size)


if __name__ == '__main__':
    main()
