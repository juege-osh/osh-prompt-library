"""Build the public, searchable index from accepted local cases."""
import json
import sqlite3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
APP = Path(__file__).resolve().parent
SOURCE = ROOT / '00_续作与清单/manifest.json'
DB = APP / 'catalog.sqlite3'


def main():
    source = json.loads(SOURCE.read_text(encoding='utf-8'))
    temporary = DB.with_suffix('.tmp')
    temporary.unlink(missing_ok=True)
    with sqlite3.connect(temporary) as db:
        db.execute('create table cases (id text primary key, title text, category text, style text, model text, generation_type text, duration real, prompt_original text, prompt_zh text, source_publisher text, source_url text, video text, poster text, input_images text, missing_reference integer, has_output_audio integer)')
        db.execute('create virtual table search using fts5(id unindexed, title, category, style, model, prompt_original, prompt_zh, tokenize="unicode61")')
        db.execute('create virtual table search_zh using fts5(id unindexed, text, tokenize="trigram")')
        count = 0
        for item in source['cases']:
            if item.get('review_status') != 'accepted':
                continue
            video = item.get('output_video', '')
            if not video or not (ROOT / video).is_file():
                continue
            poster = item.get('poster', '')
            if poster and not (ROOT / poster).is_file():
                poster = ''
            chinese = item.get('prompt_zh_full') or item.get('prompt_zh') or ''
            values = (item['id'], item.get('title', ''), item.get('category', ''), item.get('style', ''), item.get('model', ''), item.get('generation_type', ''), item.get('duration'), item.get('prompt_original', ''), chinese, item.get('source_publisher', ''), item.get('source_url', ''), video, poster, json.dumps(item.get('input_images', []), ensure_ascii=False), int(bool(item.get('missing_reference_mentioned_in_prompt'))), int(item.get('has_output_audio') is True))
            db.execute('insert into cases values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)', values)
            db.execute('insert into search values (?,?,?,?,?,?,?)', (values[0], values[1], values[2], values[3], values[4], values[7], values[8]))
            db.execute('insert into search_zh values (?,?)', (values[0], ' '.join((values[1], values[2], values[3], values[8]))))
            count += 1
        db.execute('create index cases_category on cases(category)')
        db.execute('create index cases_model on cases(model)')
        db.execute('create index cases_duration on cases(duration)')
        db.execute('create table meta (key text primary key, value text)')
        db.executemany('insert into meta values (?,?)', [('count', str(count)), ('updated_at', source.get('updated_at', ''))])
        db.execute('pragma optimize')
    temporary.replace(DB)
    print(f'Indexed {count} accepted cases: {DB}')


if __name__ == '__main__':
    main()
