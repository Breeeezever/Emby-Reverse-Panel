"""Build the self-hosted UI font subsets from Inter and Noto Sans SC TTFs.

Usage: python tools/build-ui-fonts.py /path/to/Inter.ttf /path/to/NotoSansSC.ttf
Requires fonttools[woff]. See web/static/fonts/README.md for sources/licenses.
"""
from pathlib import Path
import sys
from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

root = Path(__file__).resolve().parents[1]
static = root / 'web/static'
out = static / 'fonts'
out.mkdir(exist_ok=True)
ui_text = ''.join(path.read_text() for suffix in ('*.html', '*.js') for path in static.rglob(suffix))
latin = set(range(0x20, 0x100)) | set(range(0x2000, 0x2070)) | {0x20AC, 0x2212}
cjk = {ord(char) for char in ui_text if ord(char) >= 0x2E80}
cjk |= set(range(0x3000, 0x3040)) | set(range(0xFF00, 0xFFF0))

for source, filename, chars in [
    (sys.argv[1], 'inter-ui.woff2', latin),
    (sys.argv[2], 'noto-sans-sc-ui.woff2', cjk),
]:
    font = TTFont(source, recalcTimestamp=False)
    axes = {'wght': (400, 400, 700)}
    if any(axis.axisTag == 'opsz' for axis in font['fvar'].axes):
        axes['opsz'] = 14
    options = subset.Options()
    options.layout_features = ['*']
    options.notdef_glyph = True
    options.notdef_outline = True
    options.recalc_timestamp = False
    subsetter = subset.Subsetter(options=options)
    subsetter.populate(unicodes=chars)
    subsetter.subset(font)
    font = instantiateVariableFont(font, axes, inplace=True)
    font.flavor = 'woff2'
    font.save(out / filename)
    print(f'{filename}: {(out / filename).stat().st_size:,} bytes')
