"""Build a read-only website index from the three local image/prompt catalogs."""
import hashlib
import sqlite3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
APP = Path(__file__).resolve().parent
DB = APP / 'images_catalog.sqlite3'
SOURCES = (
    ('diffusiondb', 'DiffusionDB', ROOT / '00_续作与清单/diffusiondb/images.sqlite3'),
    ('qwen', 'Qwen-Image-Bench', ROOT / '00_续作与清单/qwen_image_bench/images.sqlite3'),
    ('tdeh', 'GPT Image 2 社区案例', ROOT / '00_续作与清单/prompts_tdeh/images.sqlite3'),
)


def thumb_path(code, row):
    if code == 'diffusiondb':
        image = Path(row['path'])
        return str(Path('00_续作与清单/diffusiondb/thumbs') / image.parent.name / (Path(row['image_name']).stem + '.webp'))
    if code == 'qwen':
        return str(Path('00_续作与清单/qwen_image_bench/thumbs') / row['model'] / (row['image_name'] + '.webp'))
    return ''


def main():
    temp = DB.with_suffix('.tmp')
    temp.unlink(missing_ok=True)
    with sqlite3.connect(temp) as out:
        out.execute('create table images (id text primary key, source_code text not null, source text not null, image_name text not null, path text not null, thumb_path text not null, sha256 text not null, bytes integer, prompt_zh text not null, prompt_original text not null, model text not null, width integer, height integer, rights_status text not null, translated integer not null)')
        out.execute('create virtual table image_search using fts5(id unindexed, prompt_zh, prompt_original, model, source, tokenize="unicode61")')
        out.execute('create virtual table image_search_zh using fts5(id unindexed, text, tokenize="trigram")')
        out.execute('create table embedded_thumbs (id text primary key, webp blob not null)')
        for code, label, source_db in SOURCES:
            if not source_db.exists():
                raise FileNotFoundError(source_db)
            with sqlite3.connect(f'file:{source_db}?mode=ro', uri=True) as src:
                src.row_factory = sqlite3.Row
                thumbs = {}
                if code == 'tdeh':
                    thumbs = dict(src.execute('select image_name,webp from thumbnails'))
                count = 0
                for row in src.execute('select * from images'):
                    image_id = code + '_' + hashlib.sha256((code + ':' + row['image_name']).encode()).hexdigest()[:24]
                    original = row['prompt_original'] or ''
                    chinese = row['prompt_zh'] or ''
                    model = row['model'] if 'model' in row.keys() else ''
                    rights = row['rights_status'] if 'rights_status' in row.keys() else 'unverified'
                    values = (image_id, code, label, row['image_name'], row['path'], thumb_path(code, row), row['sha256'], row['bytes'], chinese, original, model or '', row['width'], row['height'], rights, int(bool(chinese.strip())))
                    out.execute('insert into images values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)', values)
                    out.execute('insert into image_search values (?,?,?,?,?)', (image_id, chinese, original, model or '', label))
                    if chinese:
                        out.execute('insert into image_search_zh values (?,?)', (image_id, chinese))
                    if code == 'tdeh' and row['image_name'] in thumbs:
                        out.execute('insert into embedded_thumbs values (?,?)', (image_id, thumbs[row['image_name']]))
                    count += 1
                print(code, count, flush=True)
        out.execute('create index images_source on images(source_code)')
        out.execute('create index images_translated on images(translated)')
        out.execute('create index images_order on images(translated desc,source_code,image_name)')
        out.execute('create index images_source_order on images(source_code,translated desc,image_name)')
        out.execute('pragma optimize')
    temp.replace(DB)
    print('indexed', DB)


if __name__ == '__main__':
    main()
