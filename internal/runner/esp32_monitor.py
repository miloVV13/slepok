"""Adapt the pinned IDF monitor to Slepok's build/flash commands."""
from esp_idf_monitor import idf_monitor


class SlepokSerialMonitor(idf_monitor.SerialMonitor):
    def run_make(self, target, env_extra=None):
        idf_monitor.log.note(
            "Exit with Ctrl+] and use 'slepok build' or 'slepok flash --port PORT'. "
            "Build/flash shortcuts are disabled in this monitor."
        )


idf_monitor.SerialMonitor = SlepokSerialMonitor
idf_monitor.main()
