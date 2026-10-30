# Нативная проверка Windows → Windows и Linux ↔ Windows

Инструкция для текущей версии Slepok, профиль RP2040 / Pico.
Команды Windows выполняются в обычном PowerShell, Linux — в bash.
После каждой команды Slepok проверяйте успешное завершение: в
PowerShell `$LASTEXITCODE` должен быть `0`. При ошибке остановите
сценарий и сохраните полный вывод.

## Подготовка

- Windows 10/11 x64, желательно отдельная чистая VM для получателя.
- Linux x86-64, уже проверенная машина подходит для отправителя.
- Бинарники из одной версии исходников. Подготовлены
  `/tmp/slepok-windows.exe` (скопировать под именем `slepok.exe`) и
  `/tmp/slepok-linux` (скопировать под именем `slepok`). Файлы в `/tmp`
  лучше забрать до перезагрузки Linux.
- На отправителе для `init` нужны интернет и Git. На получателе
  нужны только бинарник и `.slepok`: Go, Git, SDK и компилятор
  устанавливать не нужно. Сборка бинарников из исходников не является
  частью проверки получателя.
- Несколько свободных гигабайт; общий слепок для двух ОС больше
  одноплатформенного. Не используйте каталог, синхронизируемый OneDrive.

`SLEPOK_HOME` не задавайте. Обычное хранилище Windows —
`%LOCALAPPDATA%\slepok`, Linux — `$XDG_DATA_HOME/slepok` или
`~/.local/share/slepok`. Для доказательства чистого восстановления
используйте новую VM / нового пользователя с пустым хранилищем.
Удалять существующее хранилище не нужно. Для каждого независимого
сценария получателя возвращайте VM к чистому состоянию либо используйте
другого нового пользователя. Уже заполненный кэш ослабляет проверку.

Отключайте виртуальный сетевой адаптер / Wi-Fi / Ethernet **до**
`validate` и `unpack`; отключения одного браузера недостаточно.
Переносите файлы через USB или подключённый диск. На Windows пока
нет встроенной сетевой изоляции, поэтому предупреждение об этом
ожидаемо даже при физически отключённой сети.

## 1. Windows → Windows

### Отправитель Windows, сеть включена

Положите `slepok.exe` в новый каталог, например `C:\slepok-test-a`.
Откройте там PowerShell:

```powershell
.\slepok.exe version
.\slepok.exe init .\win-blink
cd .\win-blink
..\slepok.exe build
Get-ChildItem .\build\*.uf2 | Get-FileHash -Algorithm SHA256
..\slepok.exe pack -o ..\win-only.slepok
cd ..
.\slepok.exe validate .\win-only.slepok
.\slepok.exe inspect .\win-only.slepok
Get-FileHash .\win-only.slepok -Algorithm SHA256
```

Ожидается: UF2 существует, сборка и проверка архива успешны;
`slepok.toml` содержит `platforms = ["windows-x64"]`, а `inspect`
показывает Windows-пакеты. Сохраните вывод с SHA256 прошивки и архива.
Исходники и `slepok.lock` после этого не меняйте.

### Получатель Windows, чистое хранилище, сеть отключена

Перенесите только `slepok.exe` и `win-only.slepok` в новый каталог,
например `C:\slepok-test-b`. Откройте там PowerShell:

```powershell
Get-FileHash .\win-only.slepok -Algorithm SHA256
.\slepok.exe validate .\win-only.slepok
.\slepok.exe unpack .\win-only.slepok .\restored
cd .\restored
..\slepok.exe build
Get-ChildItem .\build\*.uf2 | Get-FileHash -Algorithm SHA256
Test-Path .\build\compile_commands.json
..\slepok.exe list
```

Ожидается: SHA256 архива совпадает с отправителем, все команды
успешны без сети; SHA256 одноимённых UF2 совпадает с отправителем.
`Test-Path` возвращает `True`. В `compile_commands.json` пути относятся
к получателю, без путей отправителя. Если хеш UF2 отличается, сохраните
оба UF2, оба lock-файла и логи; не меняйте `sleep()` для этой проверки.

### Дополнительные проверки Windows

В восстановленном проекте выполните:

```powershell
..\slepok.exe shell
```

В открывшемся shell:

```powershell
cmake --version
cmake -S . -B shell-build
cmake --build shell-build
exit
```

Ожидается закреплённая версия CMake и успешная сборка через профиль.
После этого `shell-build` не используйте при `pack`: это дополнительный
каталог сборки, который может попасть в исходники архива.

Для Ctrl+C распакуйте архив в ещё один новый каталог и прервите
первый `build` во время компиляции. Сразу проверьте `$LASTEXITCODE`
(ожидается `130`), убедитесь в Диспетчере задач, что относящиеся к
этой сборке CMake/Ninja/GCC не продолжают работать. Затем повторите
`build`: он должен завершиться успешно. Слишком быструю сборку можно
повторить в новом восстановленном каталоге.

Отдельно повторите распаковку/сборку в пути с пробелами, например
`C:\slepok test\restored project`, указывая пути в кавычках. Для
проверки распаковки не запускайте PowerShell от администратора и
не включайте Developer Mode специально ради симлинков.

## 2. Linux → Windows: общий слепок

### Отправитель Linux, сеть включена

Положите `slepok` в новый рабочий каталог и откройте там bash:

```bash
chmod +x ./slepok
./slepok version
./slepok init --platform linux-x64,windows-x64 dual-blink
cd dual-blink
../slepok build
sha256sum build/*.uf2
../slepok pack --platform linux-x64,windows-x64 -o ../dual.slepok
cd ..
./slepok validate dual.slepok
./slepok inspect dual.slepok
sha256sum dual.slepok
```

`init --platform` загружает обе платформы и записывает lock. `inspect`
должен показать обе платформы и оба варианта SDK. Сохраните архив,
вывод, исходники и lock. Если Linux сообщает, что сетевая изоляция
недоступна, запишите это: такой `pack` сам по себе ещё не доказывает
офлайн-сборку, последующая проверка получателя обязательна.

### Получатель Windows, чистое хранилище, сеть отключена

Перенесите `dual.slepok` и `slepok.exe` в новый каталог:

```powershell
Get-FileHash .\dual.slepok -Algorithm SHA256
.\slepok.exe validate .\dual.slepok
.\slepok.exe unpack .\dual.slepok .\from-linux
cd .\from-linux
..\slepok.exe build
Get-ChildItem .\build\*.uf2 | Get-FileHash -Algorithm SHA256
Test-Path .\build\compile_commands.json
```

Ожидается успешная сборка без сети, установки инструментов и
привилегий создания симлинков. Windows должен выбрать свои пакеты.
UF2 сохраните, но совпадение SHA256 **между разными ОС** пока не
является критерием: это отдельная проверка воспроизводимости
разных host toolchain. На одной ОС для одинакового слепка хеши
должны совпадать.

### Получатель Linux, чистое хранилище, сеть отключена

Перенесите тот же `dual.slepok` и Linux-бинарник в новый каталог:

```bash
chmod +x ./slepok
sha256sum dual.slepok
./slepok validate dual.slepok
./slepok unpack dual.slepok from-dual
cd from-dual
../slepok build
sha256sum build/*.uf2
test -s build/compile_commands.json && echo 'compile_commands: OK'
```

Ожидается успех без сети и совпадение UF2 с Linux-отправителем
этого же архива. Это проверяет, что включение Windows-пакетов не
повредило Linux-вариант.

## 3. Windows → Linux: подготовка на Windows

Используйте обновлённые бинарники с поддержкой переносимых Unix-пакетов.
На Windows-отправителе, с сетью и установленным Git, в новом каталоге
рядом с `slepok.exe`:

```powershell
.\slepok.exe init --platform linux-x64,windows-x64 .\win-dual
cd .\win-dual
..\slepok.exe build
Get-ChildItem .\build\*.uf2 | Get-FileHash -Algorithm SHA256
..\slepok.exe pack --platform linux-x64,windows-x64 -o ..\from-windows-dual.slepok
cd ..
.\slepok.exe validate .\from-windows-dual.slepok
.\slepok.exe inspect .\from-windows-dual.slepok
Get-FileHash .\from-windows-dual.slepok -Algorithm SHA256
```

Ожидается успешная подготовка обеих ОС без WSL, администратора и
привилегий создания Unix-симлинков. Linux-пакеты остаются каноническими
tar внутри хранилища; Windows выполняет проверочную сборку своими
инструментами. На этом шаге Linux-компилятор не запускается.

Перенесите архив и Linux-бинарник на чистого Linux-получателя,
отключите сеть и выполните:

```bash
chmod +x ./slepok
sha256sum from-windows-dual.slepok
./slepok validate from-windows-dual.slepok
./slepok unpack from-windows-dual.slepok from-windows
cd from-windows
../slepok build
sha256sum build/*.uf2
test -s build/compile_commands.json && echo 'compile_commands: OK'
```

Ожидается успех без сети. Это проверяет восстановление Linux execute-bit,
symlink и hardlink из пакетов, подготовленных на Windows. Тот же архив
проверьте на чистом Windows-получателе командами раздела 1; Windows UF2
должен совпасть с Windows-отправителем этого архива.

Для слепка только под Linux на подготовленном Windows-отправителе
можно выполнить `pack --platform linux-x64 -o ../linux-only.slepok`.
Такой архив должен восстановиться на Linux и отклоняться на Windows.

## 4. Linux → Windows → Linux: переупаковка без сети

После получения `dual.slepok` из раздела 2 на чистой Windows,
не включая сеть, распакуйте его в новый каталог `for-repack` рядом
с бинарником. Дополнительный shell-build из раздела 1 здесь не нужен.

```powershell
.\slepok.exe unpack .\dual.slepok .\for-repack
cd .\for-repack
..\slepok.exe build
..\slepok.exe pack --platform linux-x64,windows-x64 -o ..\repacked-on-windows.slepok
cd ..
.\slepok.exe validate .\repacked-on-windows.slepok
.\slepok.exe inspect .\repacked-on-windows.slepok
```

`init` выполнять не нужно: чужие Linux-пакеты сохранены при `unpack`.
Перенесите новый архив на чистый Linux без сети и повторите команды
получателя из раздела 3, заменив имя архива и каталог назначения.
Linux UF2 должен совпасть с Linux-отправителем исходного `dual.slepok`.
В `inspect` digest пакетов до и после переупаковки должны совпадать.
SHA256 всего `.slepok` сравнивать на равенство не нужно: отчёт
проверочной сборки меняется при новой упаковке.

## 5. Ожидаемый отказ при отсутствии платформы

Прямое восстановление `win-only.slepok` на Linux сейчас **не
поддерживается**: архив не содержит Linux-инструментов. На Linux:

```bash
./slepok validate win-only.slepok
./slepok unpack win-only.slepok should-not-appear
```

Ожидается успешный `validate`, затем отказ `unpack` с указанием
несовместимой платформы; готовый каталог `should-not-appear` не
должен появиться. Это ожидаемый отказ, а не дефект.

## Что прислать после проверки

- ОС и версия, вывод `slepok version`, какие сценарии выполнены.
- Было ли хранилище получателя пустым и как отключалась сеть.
- Вывод `inspect` для обоих архивов, SHA256 архивов и UF2 с каждой ОС.
- При ошибке: точная команда, полный вывод и код возврата;
  лог по пути, который напечатал Slepok, плюс `slepok.toml` и
  `slepok.lock`. Для расхождения путей — `compile_commands.json`.
- Результаты shell, Ctrl+C и пути с пробелами; прошивку на плате
  можно проверить дополнительно, если Pico под рукой.
