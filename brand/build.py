#!/usr/bin/env python3
"""Generate Stampede's brand SVGs from the block-art bull.

Run: python3 brand/build.py
Every asset is built from the grids below, so the bull and the block
wordmark stay identical everywhere they appear.
"""

import os

BULL = [
    "#...........#",
    "##.........##",
    ".###########.",
    "..#########..",
    "..##.###.##..",
    "..#########..",
    "...#######...",
    "...#.###.#...",
    "...#######...",
    "...##...##...",
]

# 5x7 block letters for the wordmark.
FONT = {
    "S": [".####", "#....", "#....", ".###.", "....#", "....#", "####."],
    "T": ["#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."],
    "A": [".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"],
    "M": ["#...#", "##.##", "#.#.#", "#.#.#", "#...#", "#...#", "#...#"],
    "P": ["####.", "#...#", "#...#", "####.", "#....", "#....", "#...."],
    "E": ["#####", "#....", "#....", "####.", "#....", "#....", "#####"],
    "D": ["####.", "#...#", "#...#", "#...#", "#...#", "#...#", "####."],
}

TEAL_DARK = "#14B8A6"   # on dark backgrounds (the original artwork)
TEAL_LIGHT = "#0D9488"  # on light backgrounds, for contrast
INK_DARK = "#F4F4F5"
INK_LIGHT = "#18181B"
TILE = "#18181B"   # the artwork's background (zinc-900)


def runs(grid, ox=0, oy=0):
    """Merge each row's filled cells into horizontal runs: (x, y, w)."""
    out = []
    for y, row in enumerate(grid):
        x = 0
        while x < len(row):
            if row[x] == "#":
                start = x
                while x < len(row) and row[x] == "#":
                    x += 1
                out.append((ox + start, oy + y, x - start))
            else:
                x += 1
    return out


def path(rs):
    return "".join(f"M{x} {y}h{w}v1h-{w}z" for x, y, w in rs)


def word(text):
    grid = [""] * 7
    for i, ch in enumerate(text):
        for r in range(7):
            grid[r] += FONT[ch][r] + ("." if i < len(text) - 1 else "")
    return grid


def svg(w, h, body, label="Stampede", size=None):
    attrs = f' width="{size[0]}" height="{size[1]}"' if size else ""
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}"{attrs} '
            f'role="img" aria-label="{label}" shape-rendering="crispEdges">\n'
            f"<title>{label}</title>\n{body}\n</svg>\n")


HERE = os.path.dirname(os.path.abspath(__file__))


def write(name, content):
    with open(os.path.join(HERE, name), "w") as f:
        f.write(content)
    print("wrote", name)


bull = path(runs(BULL))
BW, BH = len(BULL[0]), len(BULL)

# Mark: the bull alone.
for theme, teal in (("dark", TEAL_DARK), ("light", TEAL_LIGHT)):
    write(f"mark-{theme}.svg", svg(BW, BH, f'<path fill="{teal}" d="{bull}"/>', size=(BW * 8, BH * 8)))

# Logo: bull and block wordmark, the text vertically centred on the head.
text = word("STAMPEDE")
TX, TY = BW + 3, 2
LW, LH = TX + len(text[0]), BH
txt = path(runs(text, TX, TY))
for theme, teal, ink in (("dark", TEAL_DARK, INK_DARK), ("light", TEAL_LIGHT, INK_LIGHT)):
    write(f"logo-{theme}.svg", svg(LW, LH, f'<path fill="{teal}" d="{bull}"/>\n<path fill="{ink}" d="{txt}"/>', size=(LW * 5, LH * 5)))

# App icon: the bull on a dark rounded tile (both themes use the tile).
pad_x, pad_y = 3.5, 5
IW = BW + 2 * pad_x
icon = (f'<rect width="{IW}" height="{IW}" rx="3.5" fill="{TILE}"/>\n'
        f'<path fill="{TEAL_DARK}" transform="translate({pad_x} {pad_y})" d="{bull}"/>')
write("icon-dark.svg", svg(IW, IW, icon, size=(512, 512)))
write("icon-light.svg", svg(IW, IW, icon, size=(512, 512)))

# Favicon: the tile icon, readable on light and dark browser tabs.
write("favicon.svg", svg(IW, IW, icon))

# Terminal banner for the CLI and console.
with open(os.path.join(HERE, "banner.txt"), "w") as f:
    for row in BULL:
        f.write(row.replace("#", "█").replace(".", " ").rstrip() + "\n")
print("wrote banner.txt")
