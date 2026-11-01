#!/usr/bin/env python3
"""Exercise the real locked IDF monitor on two PTYs, never physical USB.
Usage: pinned-python scripts/esp32_monitor_smoke.py /private/test-plan.json
The Go integration test supplies the real plan and controlled environment.
PTYs lack modem control lines; only DTR/RTS ioctls are simulated.
"""
import errno
import os
import pathlib
import json
import fcntl
import struct
import termios
import pty
import select
import signal
import sys
import time

plan = json.loads(pathlib.Path(sys.argv[1]).read_text())
uart_master, uart_slave = pty.openpty()
port = os.ttyname(uart_slave)
start_read, start_write = os.pipe()
pid, console = pty.fork()
if pid == 0:
    os.close(start_write)
    os.read(start_read, 1)
    os.close(start_read)
    os.chdir(plan['Directory'])
    args = plan['Args']
    args[args.index('--port') + 1] = port
    # Test-only modem emulation: real byte transport and console use PTYs.
    bootstrap = """import runpy, serial, sys
virtual_port = sys.argv[1]
original_dtr = serial.Serial._update_dtr_state
original_rts = serial.Serial._update_rts_state
serial.Serial._update_dtr_state = lambda self: None if self.port == virtual_port else original_dtr(self)
serial.Serial._update_rts_state = lambda self: None if self.port == virtual_port else original_rts(self)
sys.argv = sys.argv[2:]
runpy.run_path(sys.argv[0], run_name='__main__')
"""
    env = dict(item.split('=', 1) for item in plan['Environment'])
    os.execve(plan['Executable'], [plan['Executable'], '-c', bootstrap, port] + args, env)
fcntl.ioctl(console, termios.TIOCSWINSZ, struct.pack("HHHH", 50, 250, 0, 0))
os.close(start_read)
original_console_settings = termios.tcgetattr(console)
os.write(start_write, b'x')
os.close(start_write)
output = bytearray()
finished = False

def wait_for(marker, timeout=20, uart_message=None):
    deadline = time.monotonic() + timeout
    while marker not in output:
        if time.monotonic() > deadline:
            raise RuntimeError('Timed out waiting for ' + repr(marker))
        if uart_message is not None:
            os.write(uart_master, uart_message)
        readable, _, _ = select.select([console], [], [], 0.2)
        if readable:
            try:
                data = os.read(console, 65536)
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise
                data = b''
            if not data:
                raise RuntimeError('Monitor exited before ' + repr(marker))
            output.extend(data)

try:
    wait_for(b'Quit:')
    wait_for(b'SLEPOK_PTY_SERIAL_MESSAGE', uart_message=b'SLEPOK_PTY_SERIAL_MESSAGE\r\n')
    wait_for(b'app_main', uart_message=('SLEPOK_PC ' + plan['Address'] + '\r\n').encode())
    typed = 'Ж'.encode()
    os.write(console, typed)
    uart_input = bytearray()
    deadline = time.monotonic() + 5
    while typed not in uart_input:
        if time.monotonic() > deadline:
            raise RuntimeError('UTF-8 console input did not reach UART')
        readable, _, _ = select.select([uart_master], [], [], 0.2)
        if readable:
            uart_input.extend(os.read(uart_master, 65536))
    os.write(console, b'\x14\x06')  # Ctrl+T, Ctrl+F: disabled flash shortcut
    wait_for(b"use 'slepok build' or 'slepok flash --port PORT'")
    if plan['ExitMode'] == 'signal':
        os.kill(pid, signal.SIGTERM)
    else:
        os.write(console, b'\x1d')  # Ctrl+]: clean monitor/TTY shutdown
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        got, status = os.waitpid(pid, os.WNOHANG)
        if got:
            finished = True
            if os.waitstatus_to_exitcode(status) != 0:
                raise RuntimeError('Monitor exit status ' + str(status))
            break
        time.sleep(0.05)
    if not finished:
        raise RuntimeError('Monitor did not exit: ' + plan['ExitMode'])
    if termios.tcgetattr(console) != original_console_settings:
        raise RuntimeError('Monitor did not restore console settings')
    print('ESP32 PTY monitor passed: UART text, UTF-8 input, addr2line, disabled flash shortcut, ' + plan['ExitMode'] + ' exit, terminal restored.')
except BaseException:
    sys.stderr.buffer.write(output)
    raise
finally:
    if not finished:
        os.kill(pid, signal.SIGKILL)
        os.waitpid(pid, 0)
    for fd in (console, uart_master, uart_slave):
        os.close(fd)
