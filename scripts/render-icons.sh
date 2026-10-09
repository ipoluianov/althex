#!/bin/bash
# Renders forms/icons/*.svg to PNGs using headless Chrome
# (ImageMagick's built-in SVG renderer drops strokes):
#   <name>.png    - 32x32 for the toolbar
#   <name>-16.png - 16x16 for menus, with thicker lines so they stay crisp
set -e
cd "$(dirname "$0")/../forms/icons"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# render <svg> <size> <png>
render() {
    printf '<html><body style="margin:0;background:transparent"><img src="file://%s" width="%d" height="%d" style="display:block"></body></html>' "$1" "$2" "$2" > "$TMP/page.html"
    google-chrome --headless=new --disable-gpu --hide-scrollbars --default-background-color=00000000 \
        --force-device-scale-factor=1 --window-size="$2,$2" --screenshot="$PWD/$3" "file://$TMP/page.html" >/dev/null 2>&1
}

for f in *.svg; do
    n=${f%.svg}
    render "$PWD/$f" 32 "$n.png"
    sed 's/stroke-width="[0-9.]*"/stroke-width="1.8"/' "$f" > "$TMP/$n-16.svg"
    render "$TMP/$n-16.svg" 16 "$n-16.png"
done
