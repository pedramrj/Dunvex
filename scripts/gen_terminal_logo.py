#!/usr/bin/env python3
"""Draws the Dunvex DV emblem as 256-colour terminal art.

It reads web/public/brand/dunvex-logo-512.png (standard library only), crops
the emblem, scales it to half-block cells and writes the result into:
  - install.sh, between the '# >>> emblem' and '# <<< emblem' markers
  - cmd/dunvex/logo_art.go (big and small emblem for the SSH menu)

Run from the repository root:  python3 scripts/gen_terminal_logo.py
"""
import os, struct, zlib

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, 'web', 'public', 'brand', 'dunvex-logo-512.png')
CROP = (70, 85, 445, 340)  # the DV emblem inside the 512px logo


def read_png(path):
    data = open(path, 'rb').read()
    assert data[:8] == b'\x89PNG\r\n\x1a\n', 'not a PNG'
    pos, idat = 8, b''
    while pos < len(data):
        ln = struct.unpack('>I', data[pos:pos + 4])[0]
        typ, body = data[pos + 4:pos + 8], data[pos + 8:pos + 8 + ln]
        if typ == b'IHDR':
            w, h, depth, ctype, _, _, inter = struct.unpack('>IIBBBBB', body)
        elif typ == b'IDAT':
            idat += body
        pos += 12 + ln
    assert depth == 8 and inter == 0 and ctype in (2, 6), 'needs 8-bit RGB/RGBA, non-interlaced'
    bpp = 4 if ctype == 6 else 3
    raw, stride = zlib.decompress(idat), w * bpp
    rows, prev, i = [], bytearray(stride), 0
    for _ in range(h):
        f = raw[i]; i += 1
        cur = bytearray(raw[i:i + stride]); i += stride
        for x in range(stride):
            a = cur[x - bpp] if x >= bpp else 0
            b = prev[x]
            c = prev[x - bpp] if x >= bpp else 0
            if f == 1: cur[x] = (cur[x] + a) & 255
            elif f == 2: cur[x] = (cur[x] + b) & 255
            elif f == 3: cur[x] = (cur[x] + ((a + b) >> 1)) & 255
            elif f == 4:
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                cur[x] = (cur[x] + (a if pa <= pb and pa <= pc else b if pb <= pc else c)) & 255
        rows.append(cur); prev = cur

    def px(x, y):
        r, o = rows[y], x * bpp
        return (r[o], r[o + 1], r[o + 2], r[o + 3] if bpp == 4 else 255)
    return w, h, px


LEV = [0, 95, 135, 175, 215, 255]
PAL = [(16 + 36 * r + 6 * g + b, (LEV[r], LEV[g], LEV[b])) for r in range(6) for g in range(6) for b in range(6)]
PAL += [(232 + k, (8 + 10 * k,) * 3) for k in range(24)]


def nearest(c):
    return min(PAL, key=lambda p: 2 * (p[1][0] - c[0]) ** 2 + 4 * (p[1][1] - c[1]) ** 2 + 3 * (p[1][2] - c[2]) ** 2)[0]


def cells(cols):
    w, h, px = read_png(SRC)
    x0, y0, x1, y1 = CROP
    rows_px = 2 * round(cols * (y1 - y0) / (x1 - x0) / 2.0)
    out = []
    for ry in range(0, rows_px, 2):
        line = []
        for cx in range(cols):
            pair = []
            for sub in (ry, ry + 1):
                sx0, sx1 = x0 + (x1 - x0) * cx / cols, x0 + (x1 - x0) * (cx + 1) / cols
                sy0, sy1 = y0 + (y1 - y0) * sub / rows_px, y0 + (y1 - y0) * (sub + 1) / rows_px
                acc, n = [0, 0, 0, 0], 0
                for yy in range(int(sy0), max(int(sy0) + 1, int(sy1))):
                    for xx in range(int(sx0), max(int(sx0) + 1, int(sx1))):
                        p = px(min(xx, w - 1), min(yy, h - 1))
                        for k in range(4): acc[k] += p[k]
                        n += 1
                r, g, b, a = (v / n for v in acc)
                pair.append(-1 if a < 90 or (r > 236 and g > 236 and b > 236) else nearest((r, g, b)))
            line.append(pair)
        out.append(line)
    # trim empty columns on both sides
    used = [i for i in range(cols) if any(row[i] != [-1, -1] for row in out)]
    lo, hi = used[0], used[-1] + 1
    return [row[lo:hi] for row in out]


def ansi_rows(grid, esc):
    """One string per row; esc is how the ESC byte is written for the target language."""
    rows = []
    for row in grid:
        s, fg, bg = '', None, None
        for top, bot in row:
            if top < 0 and bot < 0:
                if bg is not None or fg is not None:
                    s += esc + '[0m'; fg = bg = None
                s += ' '
                continue
            if top >= 0 and bot >= 0 and top != bot:
                want_fg, want_bg, ch = top, bot, '▀'
            elif top >= 0 and bot >= 0:
                want_fg, want_bg, ch = top, None, '█'
            elif top >= 0:
                want_fg, want_bg, ch = top, None, '▀'
            else:
                want_fg, want_bg, ch = bot, None, '▄'
            if want_bg is None and bg is not None:
                s += esc + '[0m'; fg = bg = None
            if want_fg != fg:
                s += esc + '[38;5;%dm' % want_fg; fg = want_fg
            if want_bg is not None and want_bg != bg:
                s += esc + '[48;5;%dm' % want_bg; bg = want_bg
            s += ch
        rows.append(s.rstrip(' ') + esc + '[0m')
    return rows


def write_install(rows):
    path = os.path.join(ROOT, 'install.sh')
    text = open(path, encoding='utf-8').read()
    a, b = text.index('# >>> emblem\n'), text.index('# <<< emblem')
    body = "EMBLEM=(\n" + ''.join("  $'%s'\n" % r for r in rows) + ")\n"
    text = text[:a] + '# >>> emblem\n# generated by scripts/gen_terminal_logo.py from the Dunvex logo\n' + body + text[b:]
    open(path, 'w', encoding='utf-8', newline='\n').write(text)


def write_go(big, small):
    path = os.path.join(ROOT, 'cmd', 'dunvex', 'logo_art.go')
    def lst(rows):
        return ''.join('\t"%s",\n' % r for r in rows)
    go = ('// Code generated by scripts/gen_terminal_logo.py from the Dunvex logo. DO NOT EDIT.\n\n'
          'package main\n\n'
          '// emblemBig and emblemSmall are the DV emblem in 256-colour half blocks.\n'
          'var emblemBig = []string{\n' + lst(big) + '}\n\n'
          'var emblemSmall = []string{\n' + lst(small) + '}\n')
    open(path, 'w', encoding='utf-8', newline='\n').write(go)


if __name__ == '__main__':
    big, small = cells(44), cells(22)
    write_install(ansi_rows(big, r'\e'))
    write_go(ansi_rows(big, r'\x1b'), ansi_rows(small, r'\x1b'))
    print('emblem: big %dx%d, small %dx%d' % (len(big[0]), len(big), len(small[0]), len(small)))
