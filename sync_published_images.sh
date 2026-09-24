#!/bin/zsh
set -euo pipefail
APP="${0:A:h}"
PRIVATE="${PROMPT_PRIVATE_DIR:-/tmp/prompt-osh-private}"
PY="${PROMPT_PYTHON:-$PRIVATE/venv/bin/python}"
SSH_TARGET="${PROMPT_SSH_TARGET:?Set PROMPT_SSH_TARGET before publishing}"
SSH_PORT="${PROMPT_SSH_PORT:-22}"
REMOTE_APP="${PROMPT_REMOTE_APP:-/opt/prompt-osh/app}"
CONTROL_PATH="${PROMPT_SSH_CONTROL_PATH:-$PRIVATE/ssh-control}"
mkdir -p "$PRIVATE"
LAST=$(cat "$PRIVATE/last-published-count" 2>/dev/null || echo 0)
if (( LAST >= 109661 )); then
    exit 0
fi
"$PY" "$APP/build_published_images.py"
COUNT=$("$PY" - "$PRIVATE/images_catalog_published.sqlite3" <<'PY'
import sqlite3,sys
with sqlite3.connect(sys.argv[1]) as db:
    print(db.execute('select count(*) from images').fetchone()[0])
PY
)
if (( COUNT <= LAST )); then
    exit 0
fi
scp -o BatchMode=yes -o ControlPath="$CONTROL_PATH" -P "$SSH_PORT" "$PRIVATE/images_catalog_published.sqlite3" "$SSH_TARGET:$REMOTE_APP/images_catalog_published.sqlite3"
ssh -o BatchMode=yes -o ControlPath="$CONTROL_PATH" -p "$SSH_PORT" "$SSH_TARGET" "chmod 644 '$REMOTE_APP/images_catalog_published.sqlite3' && mv '$REMOTE_APP/images_catalog_published.sqlite3' '$REMOTE_APP/images_catalog.sqlite3' && systemctl restart prompt-osh"
print -r -- "$COUNT" > "$PRIVATE/last-published-count"
print -r -- "published=$COUNT"
