# Project Python dependencies

Declare exact root requirements in `slepok.toml`, then run `slepok init`:

```toml
[python]
requirements = ["Jinja2==3.1.6"]
```

For profiles that do not normally need Python (such as STM32), declaring these
requirements also pins and prepares the shared Python runtime. Projects without
Python requirements keep their usual tool set.

Extras are supported (`package[extra]==1.2.3`). The first init needs network
access to PyPI and uses pip from the profile's pinned Python, not a system pip.
It resolves dependencies for every configured host platform, selects only wheels
compatible with that platform and the pinned CPython, and records names,
versions, wheel URLs and SHA256 in `slepok.lock`. Commit both project files.

Subsequent init keeps the locked graph and can reuse installed packages offline.
Changing the root requirements requires another `slepok init`; build, shell and
pack reject a stale lock. Adding a platform keeps versions of common transitive
dependencies pinned by the already prepared platforms. Remove a requirement
from the list and run init to remove its old Python bundle from the new lock;
normal `slepok gc` can reclaim bundles that no live project uses.

## Build scripts

In CMake, use the Python interpreter selected by Slepok:

```cmake
find_package(Python3 REQUIRED COMPONENTS Interpreter)
add_custom_command(
    OUTPUT "${CMAKE_CURRENT_BINARY_DIR}/generated.h"
    COMMAND "${Python3_EXECUTABLE}"
            "${CMAKE_CURRENT_SOURCE_DIR}/generate.py"
            "${CMAKE_CURRENT_BINARY_DIR}/generated.h"
    DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/generate.py"
    VERBATIM)
```

Attach that output to your target using `target_sources` or a custom target
dependency. `generate.py` can import Jinja2 and its pinned dependencies normally.
`slepok shell` exposes the same interpreter and libraries, including `python -m`
entry points supplied by dependencies. No activation or manual PYTHONPATH is
required.

## Isolation and snapshots

The project libraries form a content-addressed, read-only site-packages bundle.
Slepok supplies its path to the profile's Python and disables the user site.
Bytecode writing is disabled. This uses a relocatable library overlay rather
than an absolute-path virtualenv: restoring never invokes pip or recreates a
venv. Profile libraries (such as ESP-IDF's Python libraries) remain on the
controlled Python search path; conflicting libraries must be avoided.

The bundle contains extracted libraries and their original wheels under
`.slepok-wheels/`, along with wheel metadata and license files. `slepok pack`
includes this bundle for each selected platform. `unpack`, `build` and `shell`
use restored packages without network access, including native extension wheels.

## Current limits

- PyPI wheels only: no sdists, editable installs, local paths or private indexes.
  A dependency without a compatible wheel produces an error during init.
- Root requirements must use exact versions; URLs, markers and version ranges
  are rejected. Dependency version ranges, extras and ordinary OS/Python markers
  in wheel metadata are resolved automatically.
- OS-release-specific dependency markers (`platform_release` / `platform_version`)
  are rejected because the receiver's OS release cannot be inferred at init.
- Generated console-script launchers and wheel `.data/scripts` are not installed.
  Use `python -m package` or a project Python script. Wheel data schemes other
  than `purelib`/`platlib` are unsupported.
- Python is the runtime pinned by the MCU profile (currently CPython 3.12).
  Selecting another interpreter version is a separate feature.
- Before deploying a native-extension dependency, test it on the intended OS.
  Cross-platform wheel resolution does not execute foreign binaries.

## Verification

The opt-in test resolves real Jinja2 (with native MarkupSafe) and pytest graphs
for all four supported hosts, including Windows-only colorama and Python-version
markers. It needs the native RP2040 runtime already prepared and access to PyPI:

```sh
SLEPOK_PYTHON_WHEELS_TEST=1 go test ./cmd/slepok -run '^TestRealPythonWheelResolution$' -v
```

`bash scripts/python_smoke.sh /path/to/slepok` performs the complete CMake
generator and snapshot round trip. On Linux with user namespaces it disables
network access for repeat init and for the receiver, which starts with an empty
store. The script retains its temporary artifacts and compares UF2/BIN/HEX.

See [pip's installation report](https://pip.pypa.io/en/stable/reference/installation-report/)
for the report used during dependency resolution. Reports are temporary;
the checked-in Slepok lock contains the artifact identity needed for reuse.
