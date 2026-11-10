#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Run a command in a pseudo-terminal and record what it shows, as an asciicast (v2).

    record-session.py --out FILE.cast [--title T] [--cols 100] [--rows 30]
                      [--answer 'REGEX=TEXT' ...] [--timeout 120] -- COMMAND [ARGS...]

The command thinks it is at a terminal of that size (TERM=xterm-256color, LANG=C.UTF-8 unless the
environment says otherwise), so it colours and decorates its output as it does for a person. Each
--answer is typed once, as if by that person, when the output has matched REGEX; "\\n" in TEXT is a newline.
The cast is played with "asciinema play FILE.cast", or rendered by render-casts.py.

Standard library only, so it runs wherever python3 does. The command line is not written into the cast:
only what the terminal showed (and the title).
"""
import argparse
import fcntl
import json
import os
import pty
import re
import select
import signal
import struct
import sys
import termios
import time


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True)
    ap.add_argument("--title", default="")
    ap.add_argument("--cols", type=int, default=100)
    ap.add_argument("--rows", type=int, default=30)
    ap.add_argument("--answer", action="append", default=[], metavar="REGEX=TEXT")
    ap.add_argument("--timeout", type=float, default=180.0)
    ap.add_argument("command", nargs=argparse.REMAINDER)
    args = ap.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        ap.error("no command")
    answers = []
    for a in args.answer:
        pattern, _, text = a.partition("=")
        answers.append([re.compile(pattern), text.replace("\\n", "\n").replace("\\r", "\r"), False])

    env = dict(os.environ)
    env.setdefault("TERM", "xterm-256color")
    if "UTF-8" not in env.get("LC_ALL", "") + env.get("LANG", "").upper().replace("UTF8", "UTF-8"):
        env["LANG"] = "C.UTF-8"
        env.pop("LC_ALL", None)
    env.pop("NO_COLOR", None)
    env["COLUMNS"], env["LINES"] = str(args.cols), str(args.rows)

    pid, master = pty.fork()
    if pid == 0:
        os.execvpe(command[0], command, env)
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", args.rows, args.cols, 0, 0))

    start = time.monotonic()
    header = {"version": 2, "width": args.cols, "height": args.rows, "timestamp": int(time.time()),
              "env": {"TERM": env["TERM"], "SHELL": "/bin/bash"}}
    if args.title:
        header["title"] = args.title
    seen = ""
    status = None
    with open(args.out, "w", encoding="utf-8") as cast:
        cast.write(json.dumps(header) + "\n")
        pending = b""
        while True:
            if time.monotonic() - start > args.timeout:
                os.kill(pid, signal.SIGKILL)
                os.waitpid(pid, 0)
                print("record-session: timed out after %ss" % args.timeout, file=sys.stderr)
                return 124
            ready, _, _ = select.select([master], [], [], 0.1)
            if not ready:
                continue
            try:
                data = os.read(master, 4096)
            except OSError:
                break
            if not data:
                break
            pending += data
            # Never split a multi-byte character across two events.
            try:
                text = pending.decode("utf-8")
                pending = b""
            except UnicodeDecodeError as e:
                if e.start == 0 or len(pending) - e.start > 3:
                    text, pending = pending.decode("utf-8", "replace"), b""
                else:
                    text, pending = pending[:e.start].decode("utf-8"), pending[e.start:]
            if text:
                cast.write(json.dumps([round(time.monotonic() - start, 6), "o", text]) + "\n")
                cast.flush()
                seen += text
            for answer in answers:
                if not answer[2] and answer[0].search(re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", seen)):
                    answer[2] = True
                    time.sleep(0.4)  # a person takes a moment
                    os.write(master, answer[1].encode())
                    seen = ""
        _, status = os.waitpid(pid, 0)
    return os.waitstatus_to_exitcode(status)


if __name__ == "__main__":
    sys.exit(main())
