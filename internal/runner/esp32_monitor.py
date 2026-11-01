"""Adapt the pinned IDF monitor to Slepok's build/flash commands."""
import codecs
import os
import signal
import sys
from types import SimpleNamespace

from esp_idf_monitor import idf_monitor


if os.name == "posix":
    import select

    class SlepokConsole(idf_monitor.miniterm.Console):
        """Avoid TextIOWrapper reads which can block monitor shutdown on macOS."""

        def __init__(self):
            super().__init__()
            self.decoder = codecs.getincrementaldecoder(sys.stdin.encoding or "utf-8")("replace")

        def getkey(self):
            if not select.select([self.fd], [], [], 0.2)[0]:
                return ""
            char = self.decoder.decode(os.read(self.fd, 1))
            return "\x08" if char == "\x7f" else char

    # pyserial uses super(Console, self), so its module-level Console name
    # must stay bound to the original class. Replace only IDF's module view.
    idf_monitor.miniterm = SimpleNamespace(
        Console=SlepokConsole, ConsoleBase=idf_monitor.miniterm.ConsoleBase
    )


class SlepokSerialMonitor(idf_monitor.SerialMonitor):
    def run_make(self, target, env_extra=None):
        idf_monitor.log.note(
            "Exit with Ctrl+] and use 'slepok build' or 'slepok flash --port PORT'. "
            "Build/flash shortcuts are disabled in this monitor."
        )


idf_monitor.SerialMonitor = SlepokSerialMonitor
# Unlike Ctrl+C (which belongs to the target UART), cancellation terminates
# the monitor through its finally blocks and restores the console settings.
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
idf_monitor.main()
