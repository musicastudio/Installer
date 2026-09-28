# Draws the option icons: black on clear, 128 px, drawn at 4x and scaled down. The engine tints them.
# Needs Pillow and the licensed "Analog Whispers.ttf" in the folder above (kept out of the repo).
#   python icons/make.py
import math
import os
from PIL import Image, ImageDraw, ImageFont

K = 4
HERE = os.path.dirname(os.path.abspath(__file__))
FONT = os.path.join(HERE, '..', 'Analog Whispers.ttf')


def canvas():
    im = Image.new('RGBA', (128 * K, 128 * K), (0, 0, 0, 0))
    return im, ImageDraw.Draw(im)


def R(*v):
    return [x * K for x in v]


def save(im, name):
    im.resize((128, 128), Image.LANCZOS).save(os.path.join(HERE, name + '.png'), optimize=True)


def plug(num, name, size):
    im, d = canvas()
    d.rectangle(R(40, 8, 52, 40), fill='black')                     # prongs
    d.rectangle(R(76, 8, 88, 40), fill='black')
    d.rectangle(R(20, 40, 108, 98), outline='black', width=10 * K)  # body; inside is 30..98 x 50..88
    d.polygon(R(38, 98, 90, 98, 76, 112, 52, 112), fill='black')     # neck
    d.rectangle(R(58, 110, 70, 124), fill='black')                   # cable
    font = ImageFont.truetype(FONT, size * K)
    x0, y0, x1, y1 = d.textbbox((0, 0), num, font=font, anchor='ls')  # the ink, not the line box
    d.text((64 * K - (x0 + x1) / 2, 69 * K - (y0 + y1) / 2), num, font=font, fill='black', anchor='ls')
    save(im, name)


plug('3', 'vst3', 49)
plug('2', 'vst2', 49)
plug('32', 'vst2-32', 44)

im, d = canvas()  # clap: action lines around an empty centre
for i in range(12):
    a = math.pi * i / 6 - math.pi / 2
    r0, r1 = (22, 60) if i % 2 == 0 else (30, 48)
    d.line(R(64 + r0 * math.cos(a), 64 + r0 * math.sin(a), 64 + r1 * math.cos(a), 64 + r1 * math.sin(a)), fill='black', width=10 * K)
save(im, 'clap')

im, d = canvas()  # au: a sine in a rounded square
d.rounded_rectangle(R(10, 10, 118, 118), radius=20 * K, outline='black', width=10 * K)
pts = [(x, 64 - 24 * math.sin(2 * math.pi * (x - 28) / 72)) for x in range(28, 101, 2)]
d.line([c * K for p in pts for c in p], fill='black', width=10 * K, joint='curve')
save(im, 'au')

im, d = canvas()  # standalone: an app window holding a keyboard
d.rectangle(R(8, 16, 120, 112), outline='black', width=10 * K)
d.rectangle(R(8, 16, 120, 40), fill='black')
x0, x1, y0, y1 = 18, 110, 50, 102
for k in (1, 2, 3, 4):
    x = x0 + (x1 - x0) * k / 5
    d.rectangle(R(x - 2, y0, x + 2, y1), fill='black')
    if k != 3:
        d.rectangle(R(x - 7, y0, x + 7, y0 + 30), fill='black')
save(im, 'standalone')

im, d = canvas()  # updater: a clockwise arrow
d.arc(R(20, 20, 108, 108), start=120, end=390, fill='black', width=10 * K)
t = math.radians(30)
px, py = 64 + 44 * math.cos(t), 64 + 44 * math.sin(t)
T, N = (-math.sin(t), math.cos(t)), (math.cos(t), math.sin(t))
d.polygon(R(px + T[0] * 20, py + T[1] * 20,
            px - T[0] * 2 + N[0] * 17, py - T[1] * 2 + N[1] * 17,
            px - T[0] * 2 - N[0] * 17, py - T[1] * 2 - N[1] * 17), fill='black')
save(im, 'updater')
