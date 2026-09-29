# Draws product/map.png, the box the installer spins (and musica.studio shows): a PC big box of
# 19 x 24 x 5 units at 40 px per unit, unfolded as a cross (ui.go, sides):
#
#         [ top  ]
#   [L ][ front ][R ][ back ]
#         [bottom]
#
# The front carries the wordmark and the editor, the back the description and four more pages,
# both spines the wordmark. The screenshots are rendered from FSVR's skin by Hollow's hollow-render
# (built by FSVR's `build.bat plugin`), so they follow the skin; the wordmark is fsvr_wordmark.svg,
# rasterized by ImageMagick. Needs Pillow, ImageMagick and the licensed "Analog Whispers.ttf" in the
# folder above.
#   python product/make_map.py [path to FSVR]      (default ../FSVR)
import os
import subprocess
import sys
import tempfile
from PIL import Image, ImageDraw, ImageFilter, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
FSVR = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, '..', '..', 'FSVR'))
FONT = os.path.join(HERE, '..', 'Analog Whispers.ttf')
U = 40
BW, BH, BD = 19 * U, 24 * U, 5 * U
BOX, INK, DARK, LIGHT = (0x95, 0xb5, 0xb7), (0x01, 0x5b, 0x8e), (0x10, 0x2c, 0x3a), (0xd7, 0xe6, 0xe7)

TMP = tempfile.mkdtemp()


def font(px):
    return ImageFont.truetype(FONT, px)


def wordmark(width, rgb):
    svg = open(os.path.join(HERE, 'fsvr_wordmark.svg'), encoding='utf-8').read().replace('#015b8e', '#%02x%02x%02x' % rgb)
    src, out = os.path.join(TMP, 'w.svg'), os.path.join(TMP, 'w.png')
    open(src, 'w', encoding='utf-8').write(svg)
    subprocess.run(['magick', '-background', 'none', '-density', '600', src, out], check=True)
    im = Image.open(out).convert('RGBA')
    im = im.crop(im.getchannel('A').getbbox())
    return im.resize((width, round(im.height * width / im.width)), Image.LANCZOS)


def shot(view, width):
    render = os.path.join(FSVR, 'build', 'x64', 'hollow', 'Release', 'hollow-render.exe')
    if not os.path.exists(render):
        render = os.path.join(FSVR, 'build', 'plugin', 'hollow', 'hollow-render')
    state, out = os.path.join(TMP, view + '.txt'), os.path.join(TMP, view + '.png')
    open(state, 'w', newline='\n').write('hollow-state 1\nui {"embeds":{"pages":{"view":"%s"}}}\n' % view)
    subprocess.run([render, os.path.join(FSVR, 'plugin', 'skin'), 'main', out, state], check=True)
    im = Image.open(out).convert('RGB')
    return im.resize((width, round(im.height * width / im.width)), Image.LANCZOS)


def framed(dst, im, x, y):
    """A screenshot with a thin ink rule and a soft shadow below it."""
    sh = Image.new('RGBA', (im.width + 40, im.height + 40), (0, 0, 0, 0))
    ImageDraw.Draw(sh).rectangle((20, 26, 20 + im.width, 26 + im.height), fill=(0, 0, 0, 90))
    sh = sh.filter(ImageFilter.GaussianBlur(9))
    dst.alpha_composite(sh, (x - 20, y - 20))
    ImageDraw.Draw(dst).rectangle((x - 3, y - 3, x + im.width + 2, y + im.height + 2), fill=INK + (255,))
    dst.paste(im, (x, y))


def centre(d, text, f, cx, y, fill):
    d.text((cx - f.getlength(text) / 2, y), text, font=f, fill=fill)


def wrap(text, f, width):
    lines, line = [], ''
    for w in text.split():
        if line and f.getlength(line + ' ' + w) > width:
            lines.append(line)
            line = w
        else:
            line = (line + ' ' + w).strip()
    return lines + [line]


def publisher(d, f, cx, y, a, b):
    """musica.studio in its two tones, centred on cx."""
    x = cx - f.getlength('musica.studio') / 2
    d.text((x, y), 'musica.', font=f, fill=a)
    d.text((x + f.getlength('musica.'), y), 'studio', font=f, fill=b)


def spine(w, h):
    """A spine's art upright (width = the box's height), turned into place by the caller."""
    im = Image.new('RGBA', (h, w), BOX + (255,))
    d = ImageDraw.Draw(im)
    wm = wordmark(round(h * 0.42), INK)
    im.alpha_composite(wm, (70, (w - wm.height) // 2))
    f = font(46)
    x = h - 70 - f.getlength('musica.studio')
    y = (w - 46) // 2 - 4
    d.text((x, y), 'musica.', font=f, fill=INK)
    d.text((x + f.getlength('musica.'), y), 'studio', font=f, fill=DARK)
    return im


def main():
    m = Image.new('RGBA', (2 * BD + 2 * BW, 2 * BD + BH), BOX + (255,))
    d = ImageDraw.Draw(m)

    # front
    fx, fy = BD, BD
    wm = wordmark(600, INK)
    m.alpha_composite(wm, (fx + (BW - wm.width) // 2, fy + 40))
    centre(d, 'Formant Synthesizer Virtual Rack', font(38), fx + BW // 2, fy + 64 + wm.height, INK)
    main_shot = shot('page_library', BW - 80)
    framed(m, main_shot, fx + 40, fy + 196)
    y = fy + 196 + main_shot.height + 34
    centre(d, 'The Yamaha FS1R, rebuilt.', font(38), fx + BW // 2, y + 20, DARK)
    y += 92
    formats = ['vst3', 'clap', 'au', 'vst2', 'standalone']
    names = ['VST3', 'CLAP', 'AU', 'VST2', 'standalone']
    gap = (BW - 80) // len(formats)
    for i, (k, n) in enumerate(zip(formats, names)):
        icon = Image.open(os.path.join(HERE, '..', 'icons', k + '.png')).convert('RGBA').resize((64, 64), Image.LANCZOS)
        tint = Image.new('RGBA', icon.size, INK + (255,))
        tint.putalpha(icon.getchannel('A'))
        cx = fx + 40 + gap * i + gap // 2
        m.alpha_composite(tint, (cx - 32, y + 8))
        centre(d, n, font(22), cx, y + 78, INK)
    d.rectangle((fx, fy + BH - 90, fx + BW - 1, fy + BH - 1), fill=INK)
    publisher(d, font(44), fx + BW // 2, fy + BH - 68, (255, 255, 255), LIGHT)

    # back
    bx, by = 2 * BD + BW, BD
    wm = wordmark(260, INK)
    m.alpha_composite(wm, (bx + 40, by + 44))
    f = font(25)
    text = ('FSVR is the Yamaha FS1R as a plug-in and a standalone. Its control logic is the unit\'s own '
            'firmware, rewritten in C++ from the decompiled v1.20 ROM, and its two custom chips are modelled '
            'and calibrated against recordings of a real FS1R.')
    y = by + 60 + wm.height
    for line in wrap(text, f, BW - 80):
        d.text((bx + 40, y), line, font=f, fill=DARK)
        y += 34
    y += 22
    tw = (BW - 80 - 24) // 2
    for i, v in enumerate(('page_parts', 'page_all_ops', 'page_quick', 'page_filter')):
        s = shot(v, tw)
        framed(m, s, bx + 40 + (i % 2) * (tw + 24), y + (i // 2) * (s.height + 24))
    y += 2 * (s.height + 24) + 14
    f = font(24)
    for line in ('88 algorithms, 8 voiced and 8 unvoiced operators',
                 'Four parts, the filter, both LFOs and the effects',
                 '1408 voices, 384 performances and 90 Fseqs built in',
                 'Formant sequences with every loop mode'):
        d.rectangle((bx + 44, y + 9, bx + 52, y + 17), fill=INK)
        d.text((bx + 66, y), line, font=f, fill=DARK)
        y += 34
    d.rectangle((bx, by + BH - 90, bx + BW - 1, by + BH - 1), fill=INK)
    f = font(24)
    d.text((bx + 40, by + BH - 72), 'Windows, macOS and Linux', font=f, fill=(255, 255, 255))
    d.text((bx + 40, by + BH - 42), 'Free software, GPLv3', font=f, fill=LIGHT)
    f = font(36)
    x = bx + BW - 40 - f.getlength('musica.studio')
    d.text((x, by + BH - 64), 'musica.', font=f, fill=(255, 255, 255))
    d.text((x + f.getlength('musica.'), by + BH - 64), 'studio', font=f, fill=LIGHT)

    # spines: reading top to bottom on both, as on a shelf
    s = spine(BD, BH).rotate(-90, expand=True)
    m.alpha_composite(s, (0, BD))
    m.alpha_composite(s, (BD + BW, BD))

    # top, upright seen from the front
    wm = wordmark(360, INK)
    m.alpha_composite(wm, (BD + (BW - wm.width) // 2, (BD - wm.height) // 2 - 14))
    centre(d, 'Formant Synthesizer Virtual Rack', font(26), BD + BW // 2, (BD + wm.height) // 2 + 4, DARK)

    m.convert('RGB').save(os.path.join(HERE, 'map.png'), optimize=True)
    print('map.png', m.size)


if __name__ == '__main__':
    main()
