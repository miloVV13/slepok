# Закреплённые Windows-пакеты RP2040

Проверено 2026-10-30. Архивы скачаны из официальных релизов,
проверены по SHA-256 и подготовлены через `windows-v1` кодом Slepok.
Полные URL, vendor sha256 и package digest находятся в
[rp2040.toml](../internal/profile/profiles/rp2040.toml).

| Компонент | Версия | Начало package digest | Проверенный путь |
|---|---|---|---|
| ARM GCC | 14.2.rel1 | `74c858e2fcf05eac` | `bin/arm-none-eabi-gcc.exe`, `g++.exe`, `objcopy.exe`, `libexec/.../cc1.exe`, `cc1plus.exe` |
| CMake | 4.3.6 | `377792190fca0a90` | `bin/cmake.exe` |
| Ninja | 1.13.2 | `6a366fc97b128795` | `ninja.exe` |
| Python | 3.12.16+20261026 | `b5a68c7ea4578d0f` | `python.exe`, `python312.dll`, `vcruntime140.dll`, `vcruntime140_1.dll` |
| pioasm | 2.3.1 | `99de3fa086f595ef` | `pioasm/pioasm.exe`, `pioasm/pioasmConfig.cmake` |
| picotool | 2.3.1 | `dcce89837bccdcd0` | `picotool/picotool.exe`, `picotool/picotoolConfig.cmake` |
| Pico SDK | 2.3.1 | `b703458527aae69f` | Отдельно проверены полный SDK и digest после распаковки |

## Происхождение checksum

- Arm: [опубликованный checksum ZIP](https://armkeil.blob.core.windows.net/developer/Files/downloads/gnu/14.2.rel1/binrel/arm-gnu-toolchain-14.2.rel1-mingw-w64-x86_64-arm-none-eabi.zip.sha256asc).
  ZIP не имеет общего верхнего каталога: `strip = 0`, в отличие от
  Linux-архива. Применение Linux-правила `strip = 1` отвергнуто тестом
  обязательных путей.
- Для остальных архивов использован `digest` asset из публичных метаданных
  официального релиза: [CMake](https://api.github.com/repos/Kitware/CMake/releases/tags/v4.3.6),
  [Ninja](https://api.github.com/repos/ninja-build/ninja/releases/tags/v1.13.2),
  [Python](https://api.github.com/repos/astral-sh/python-build-standalone/releases/tags/20261026),
  [SDK tools](https://api.github.com/repos/raspberrypi/pico-sdk-tools/releases/tags/v2.3.1-0).
  Скачанные байты совпали с этими checksum; вход в GitHub не использовался.

## Проверка содержимого

`TestPrepareWindowsPackages` проверяет vendor checksum при скачивании,
digest после установки и повторного экспорта, требуемые пути и PE AMD64.
Тест читает импорты ключевых PE; отдельная инспекция через `objdump -p`
дала тот же набор библиотек для GCC, CMake, Ninja, Python и SDK tools.

Python требует VCRUNTIME140; соответствующие DLL присутствуют в пакете.
Прямые импорты проверенных GCC/CMake/Ninja/SDK tools ссылаются на библиотеки
Windows, включая UCRT API sets. Это инспекция файлов, а не доказательство
запуска на чистой Windows: отложенные импорты, расширения Python и
фактический поиск DLL требуют нативной проверки.

```sh
SLEPOK_WINDOWS_PACKAGES_OUT=/tmp/slepok-windows-packages \
  go test -v ./internal/fetch -run '^TestPrepareWindowsPackages$' -count=1
```

Окружение и загрузки остаются по указанному пути; тест пишет
`windows-digests.json`. Windows-программы на Linux не запускаются.
Обычный `go test ./...` пропускает эту сетевую проверку.

## Запуск и lock-файл

В `[hosts.windows-x64]` Python находится в корне пакета; `Python3_EXECUTABLE`
и `CMAKE_MAKE_PROGRAM` заданы явно; пользовательские Python-пакеты
отключены. Общие Linux-настройки и пакеты сохранены. Go обеспечивает
`SYSTEMROOT`; для сборки передаются `TEMP` и `TMP`. Git на Windows
вызывается с `core.longpaths=true` без изменения настроек пользователя.
Проверка хостовых зависимостей учитывает Windows drive/UNC пути,
регистр, разделители и экранирование Ninja.

`init --platform linux-x64,windows-x64` на Linux выбирает платформы без
ручного редактирования TOML. При добавлении платформы повторный `init` дополняет lock,
сохраняя прежние источники и `source_date_epoch`. Если профиль предлагает
для нового хоста другую версию закреплённого компонента, операция
отказывается смешивать версии. `pack` отклоняет неполный набор компонентов.

Для мультиплатформенной подготовки сейчас следует использовать Linux:
на Windows Unix-метаданные Linux-пакетов пока не воспроизводятся.
Общий архив создаётся `pack --platform linux-x64,windows-x64`;
без флага `pack` включает только текущую ОС.
Windows → Windows использует только `windows-v1`. Shell и отмена
дерева процессов реализованы; их нативная приёмка ещё не пройдена.
Порядок проверки: [Windows → Windows и Linux ↔ Windows](NATIVE_ACCEPTANCE.md).
