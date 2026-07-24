#!/usr/bin/env python3
"""Upload game art into the Postgres `game_assets` blob table.

Converts each image in a directory to WebP (quality 82) and prints one
`INSERT … ON CONFLICT` statement per file (base64-encoded) to stdout. Pipe it to
a psql — prod over SSH, or a local DB:

    # prod (hardened SSH alias `psycho`):
    python3 deploy/upload-game-assets.py ~/Desktop/vanya_assets \\
        | ssh psycho "sudo -u postgres psql psychospace"

    # local dev DB:
    python3 deploy/upload-game-assets.py ~/Desktop/vanya_assets \\
        | psql "postgres://psychospace:psychospace@localhost:5432/psychospace"

The art_key is the filename without extension and must match an art key in
internal/game/content.go. Re-running upserts. Requires Pillow (pip install pillow).

Usage: upload-game-assets.py [dir] [game_key]
  dir       default ~/Desktop/vanya_assets
  game_key  default smalltalk_khimki
"""

import base64
import glob
import io
import os
import signal
import sys

from PIL import Image

# Behave like a normal Unix filter when the reader closes early (e.g. `| head`).
signal.signal(signal.SIGPIPE, signal.SIG_DFL)

SRC = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/Desktop/vanya_assets")
GAME = sys.argv[2] if len(sys.argv) > 2 else "smalltalk_khimki"
EXTS = ("*.jpg", "*.jpeg", "*.png", "*.webp")


def lit(s: str) -> str:
    return "'" + s.replace("'", "''") + "'"


files = sorted(f for pat in EXTS for f in glob.glob(os.path.join(SRC, pat)))
if not files:
    sys.exit(f"no images (*.jpg/*.jpeg/*.png/*.webp) in {SRC}")

print("BEGIN;")
for f in files:
    key = os.path.splitext(os.path.basename(f))[0]
    buf = io.BytesIO()
    Image.open(f).convert("RGB").save(buf, "WEBP", quality=82, method=6)
    b64 = base64.b64encode(buf.getvalue()).decode()
    print(
        "INSERT INTO game_assets (game_key, art_key, content_type, bytes) VALUES ("
        f"{lit(GAME)}, {lit(key)}, 'image/webp', decode('{b64}', 'base64')) "
        "ON CONFLICT (game_key, art_key) DO UPDATE SET "
        "bytes = EXCLUDED.bytes, content_type = EXCLUDED.content_type, updated_at = now();"
    )
    print(f"-- {GAME}/{key}: {len(buf.getvalue()) // 1024} KB webp", file=sys.stderr)
print("COMMIT;")
