#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Turn a directory of asciicasts into one web page that shows what each ended up looking like.

    render-casts.py DIR          # writes DIR/index.html from DIR/*.cast
    render-casts.py --text FILE  # prints what FILE's screen ended up showing, as plain text

No JavaScript and nothing fetched: coloured text in a page, so the page opens anywhere from a downloaded
artifact. The casts themselves play with "asciinema play". Each session is replayed through a small
terminal model (carriage returns, erase-to-end-of-line, colours), so a spinner that was cleared is not in
the picture, as it was not on the screen at the end.
"""
import html
import json
import os
import re
import sys

TOKEN = re.compile(r"\x1b\[([0-9;?]*)([A-Za-z])|([\x00-\x1f])|([^\x1b\x00-\x1f]+)")
COLOURS = {30: "#3b3b3b", 31: "#e5534b", 32: "#57ab5a", 33: "#c69026", 34: "#539bf5", 35: "#b083f0", 36: "#39c5cf", 37: "#cdd9e5"}


class Screen:
    """Lines of (character, style) cells, written the way a terminal writes them."""

    def __init__(self):
        self.lines = [[]]
        self.row = 0
        self.col = 0
        self.bold = self.dim = False
        self.fg = None

    def style(self):
        return (self.bold, self.dim, self.fg)

    def put(self, text):
        for ch in text:
            line = self.lines[self.row]
            while len(line) < self.col:
                line.append((" ", (False, False, None)))
            cell = (ch, self.style())
            if self.col < len(line):
                line[self.col] = cell
            else:
                line.append(cell)
            self.col += 1

    def control(self, c):
        if c == "\n":
            self.row += 1
            while len(self.lines) <= self.row:
                self.lines.append([])
            self.col = 0
        elif c == "\r":
            self.col = 0
        elif c == "\b":
            self.col = max(0, self.col - 1)

    def csi(self, params, final):
        nums = [int(p) if p.isdigit() else 0 for p in params.split(";")] if params and "?" not in params else []
        if final == "m":
            for n in nums or [0]:
                if n == 0:
                    self.bold = self.dim = False
                    self.fg = None
                elif n == 1:
                    self.bold = True
                elif n == 2:
                    self.dim = True
                elif n == 22:
                    self.bold = self.dim = False
                elif n in COLOURS:
                    self.fg = n
                elif n == 39:
                    self.fg = None
        elif final == "K":
            del self.lines[self.row][self.col:]
        elif final == "A":
            self.row = max(0, self.row - (nums[0] if nums and nums[0] else 1))
        elif final == "G":
            self.col = max(0, (nums[0] if nums else 1) - 1)

    def feed(self, text):
        for m in TOKEN.finditer(text):
            if m.group(2):
                self.csi(m.group(1), m.group(2))
            elif m.group(3):
                self.control(m.group(3))
            elif m.group(4):
                self.put(m.group(4))

    def html(self):
        out = []
        lines = self.lines
        while lines and not lines[-1]:
            lines = lines[:-1]
        for line in lines:
            parts, run, run_style = [], "", None
            for ch, style in line + [("", None)]:
                if style != run_style and run:
                    parts.append(span(run, run_style))
                    run = ""
                run_style = style
                run += ch
            out.append("".join(parts))
        return "\n".join(out)


def span(text, style):
    bold, dim, fg = style
    css = []
    if fg:
        css.append("color:" + COLOURS[fg])
    if bold:
        css.append("font-weight:bold")
    if dim:
        css.append("opacity:.6")
    text = html.escape(text)
    return '<span style="%s">%s</span>' % (";".join(css), text) if css else text


def read_cast(path):
    """The header and the events; a last line that is not whole yet (the cast is still being written) is left out."""
    with open(path, encoding="utf-8") as f:
        header = json.loads(f.readline())
        events = []
        for line in f:
            try:
                events.append(json.loads(line))
            except ValueError:
                break
    return header, events


def screen_text(path):
    """What the screen shows at the end of a cast, as plain text."""
    _, events = read_cast(path)
    screen = Screen()
    for _, kind, data in events:
        if kind == "o":
            screen.feed(data)
    lines = ["".join(ch for ch, _ in line).rstrip() for line in screen.lines]
    while lines and not lines[-1]:
        lines.pop()
    return "\n".join(lines) + "\n"


def render(directory):
    sections = []
    for name in sorted(os.listdir(directory)):
        if not name.endswith(".cast"):
            continue
        header, events = read_cast(os.path.join(directory, name))
        screen = Screen()
        for _, kind, data in events:
            if kind == "o":
                screen.feed(data)
        title = header.get("title") or name[: -len(".cast")]
        seconds = events[-1][0] if events else 0
        sections.append(
            '<section><h2>%s</h2><p class="meta">%s, %dx%d, %.1f s &middot; <code>asciinema play %s</code></p>'
            "<pre>%s</pre></section>"
            % (html.escape(title), html.escape(name), header.get("width", 0), header.get("height", 0), seconds,
               html.escape(name), screen.html())
        )
    page = (
        "<!doctype html><meta charset=utf-8><title>synctang: what the programs look like</title>"
        "<style>body{background:#22272e;color:#cdd9e5;font:15px/1.3 system-ui,sans-serif;margin:2em auto;max-width:70em;padding:0 1em}"
        "pre{background:#1c2128;padding:1em;overflow:auto;font:14px/1.25 ui-monospace,Menlo,Consolas,monospace;border-radius:6px}"
        ".meta{color:#768390;margin:0 0 .4em}h2{margin:1.6em 0 .2em}code{background:#2d333b;padding:0 .3em}</style>"
        "<h1>synctang: what the programs look like</h1>"
        "<p>Recorded in the end-to-end test, at a terminal, on both ends. What each session ended up showing; "
        "play the <code>.cast</code> files with <code>asciinema play</code> to see it happen.</p>" + "".join(sections)
    )
    path = os.path.join(directory, "index.html")
    with open(path, "w", encoding="utf-8") as f:
        f.write(page)
    return path


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--text":
        sys.stdout.write(screen_text(sys.argv[2]))
    elif len(sys.argv) == 2:
        print(render(sys.argv[1]))
    else:
        sys.exit(__doc__)
