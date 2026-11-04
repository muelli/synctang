#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Holds the serial console of the end-to-end test VM (scripts/e2e-vm.sh).

Everything the console prints goes to a log file, which the test searches; every
line written to a FIFO is typed on the console. It connects once and stays for
the whole test, because QEMU does not replay what a console missed, and the VM
reboots several times on the same connection.

usage: e2e-console.py PORT LOGFILE FIFO
"""
import os
import socket
import sys
import threading
import time


def connect(port, seconds=60):
    deadline = time.time() + seconds
    while True:
        try:
            return socket.create_connection(("127.0.0.1", port))
        except OSError:
            if time.time() > deadline:
                raise
            time.sleep(0.5)


def main():
    port, logfile, fifo = int(sys.argv[1]), sys.argv[2], sys.argv[3]
    sock = connect(port)

    def copy_out():
        with open(logfile, "ab", buffering=0) as log:
            while True:
                data = sock.recv(4096)
                if not data:
                    return
                log.write(data)

    threading.Thread(target=copy_out, daemon=True).start()
    # O_RDWR so that the FIFO has a writer (this process) and never reports end of file
    # when a test finishes writing a line.
    fd = os.open(fifo, os.O_RDWR)
    with os.fdopen(fd, "rb", buffering=0) as lines:
        buf = b""
        while True:
            chunk = lines.read(1024)
            if not chunk:
                return
            buf += chunk
            while b"\n" in buf:
                line, buf = buf.split(b"\n", 1)
                sock.sendall(line + b"\r")


if __name__ == "__main__":
    main()
