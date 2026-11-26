#!/bin/sh
# puts the website together in one folder, the github pages workflow runs this too
#   sh site/build.sh [out]   default out is _site
# the logo, fonts, icons and screenshots are copied from the repo as they are, nothing is redrawn
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT=${1:-$ROOT/_site}
rm -rf "$OUT"
mkdir -p "$OUT/assets/fonts" "$OUT/assets/shots"
cp "$ROOT/site/index.html" "$ROOT/site/style.css" "$ROOT/site/app.js" "$OUT/"
cp "$ROOT/docs/logo.svg" "$ROOT/docs/social-preview.png" "$OUT/assets/"
cp "$ROOT/internal/web/static/img/icons.svg" "$ROOT/internal/web/static/img/LICENSE-lucide.txt" "$OUT/assets/"
cp "$ROOT"/internal/web/static/fonts/*.woff2 "$ROOT/internal/web/static/fonts/LICENSE-IBM-Plex.txt" "$OUT/assets/fonts/"
for s in dashboard dashboard-light devices profiles activity filtering check-site blockpage blockpage-phone mytime-phone; do
	cp "$ROOT/docs/screenshots/$s.png" "$OUT/assets/shots/"
done
# github pages would otherwise run it through jekyll
touch "$OUT/.nojekyll"
echo "site built in $OUT"
