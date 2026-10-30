# Пакеты RP2040 для macOS

Профиль использует прежние версии GCC, CMake, Ninja, Python и Pico SDK.
Новые платформы: `macos-arm64` и `macos-x64`. Полные vendor SHA256,
URL и package digest закреплены в [rp2040.toml](../internal/profile/profiles/rp2040.toml).
Linux/Windows digest и формат слепка не изменены.

| Компонент | Представление macOS | Начало package digest |
|---|---|---|
| ARM GCC 14.2.rel1 | Apple Silicon | `26413b74839436b4` |
| ARM GCC 14.2.rel1 | Intel, оригинальный архив Arm | `d2c524a3057cdb0b` |
| CMake 4.3.6 | Universal, `CMake.app/Contents/bin` | `f91983036470c133` |
| Ninja 1.13.2 | Universal | `b5548b4c17cbcd28` |
| Python 3.12.16+20261026 | Apple Silicon | `abad5317ce077b58` |
| Python 3.12.16+20261026 | Intel | `2b24ba80a4cce374` |
| pioasm 2.3.1 | Universal | `90094e3374d50905` |
| picotool 2.3.1 + libusb | Universal | `6c6ba3d4b4325826` |
| Pico SDK 2.3.1 | Прежний Unix / `any` | `b6f7eccbf2159b28` |
| zstd 1.5.7_1 | Runtime только для Intel GCC | `a708bb97af79d575` |

Одинаковые universal-пакеты в слепке для двух Mac-архитектур хранятся
один раз как общий fallback. Linux/Windows-пакеты с тем же именем
сохраняют приоритет на своих ОС.

## Intel GCC и zstd

Официальный Intel GCC 14.2 требует
`/usr/local/opt/zstd/lib/libzstd.1.dylib` в `cc1`/`cc1plus`. Простая
перепаковка архива оставляет зависимость от установленного Homebrew.
Slepok включает отдельную библиотеку zstd, ограниченную платформой
`macos-x64`. Checksum бутылки взят из
[официального API Homebrew](https://formulae.brew.sh/api/formula/zstd.json);
зеркала служат только доставкой тех же проверяемых байтов.
Homebrew не устанавливается и не запускается. В профиле используется
библиотека, а не CLI из бутылки. Выбранная бутылка рассчитана на macOS 15.

Slepok создаёт локальные обёртки GCC, которые выставляют путь к
упакованной библиотеке непосредственно перед запуском компилятора.
Это учитывает удаление `DYLD_*` при входе в системный `/bin/sh` через
SIP. Глобальные переменные и настройки безопасности не меняются;
vendor Mach-O и подписи не модифицируются. Обёртки — производный
кэш: после GC и на новом получателе они создаются заново, со стабильным
для данного хранилища путём. Они не входят в данные слепка.

## Проверки

`TestPrepareMacOSPackages` проверяет SHA256 загрузок, канонические
digest, ожидаемые executable-файлы, CPU Mach-O и dylib-зависимости
ключевых инструментов. Для Intel GCC отдельно допускается только
известная зависимость zstd, которую предоставляет обязательный runtime.
Также проверяются обе архитектуры `libusb` и библиотека zstd.

```sh
SLEPOK_MACOS_PACKAGES_OUT=/absolute/path/to/store \
  go test -v ./internal/fetch -run '^TestPrepareMacOSPackages$' -count=1 -timeout 45m
```

Это опциональная сетевая проверка; обычные тесты не скачивают toolchain.
Отчёт сохраняется в `macos-digests.json`. Статическая инспекция на Linux
не заменяет нативный `scripts/macos_smoke.sh` и чистого получателя без сети.

Источники: [checksum Arm, Apple Silicon](https://armkeil.blob.core.windows.net/developer/Files/downloads/gnu/14.2.rel1/binrel/arm-gnu-toolchain-14.2.rel1-darwin-arm64-arm-none-eabi.tar.xz.sha256asc),
[checksum Arm, Intel](https://armkeil.blob.core.windows.net/developer/Files/downloads/gnu/14.2.rel1/binrel/arm-gnu-toolchain-14.2.rel1-darwin-x86_64-arm-none-eabi.tar.xz.sha256asc),
[CMake](https://api.github.com/repos/Kitware/CMake/releases/tags/v4.3.6),
[Ninja](https://api.github.com/repos/ninja-build/ninja/releases/tags/v1.13.2),
[Python](https://api.github.com/repos/astral-sh/python-build-standalone/releases/tags/20261026),
[Pico tools](https://api.github.com/repos/raspberrypi/pico-sdk-tools/releases/tags/v2.3.1-0).
