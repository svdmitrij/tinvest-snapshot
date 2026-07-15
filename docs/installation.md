# Установка tinvest-snapshot

## Требования

### Готовый бинарь (без сборки)

**Linux amd64:**
- X11 или Wayland (XWayland) — требуется для GUI
- Библиотеки OpenGL (обычно уже установлены в desktop-дистрибутивах:
  `libgl1-mesa-dev` на Debian/Ubuntu, `mesa-libGL-devel` на Fedora/RHEL)
- Сторонние рантаймы (Go, Fyne, Java, .NET) **не требуются** — бинарь
  статический

**Windows amd64:**
- Windows 10 или новее
- Сторонние рантаймы **не требуются** — бинарь включает Go/Fyne runtime

### Сборка из исходников

- **Go 1.25+** (GUI требует CGO)
- **Linux:** заголовки X11 и OpenGL: `sudo apt install xorg-dev libgl1-mesa-dev`
  (Debian/Ubuntu) или `sudo dnf install libX11-devel mesa-libGL-devel`
  (Fedora/RHEL)
- **Кросс-сборка под Windows:** `x86_64-w64-mingw32-gcc` (пакет `mingw-w64`):
  `sudo apt install mingw-w64` (Debian/Ubuntu) или
  `sudo dnf install mingw64-gcc` (Fedora)

## Установка GUI

### Готовый бинарь

1. Скачайте архив для вашей платформы из [релизов](https://github.com/svdmitrij/tinvest-snapshot/releases):
   - `tinvest-gui-v<версия>-linux-amd64.tar.gz` — Linux
   - `tinvest-gui-v<версия>-windows-amd64.zip` — Windows
2. Распакуйте архив в удобный каталог.
3. Скопируйте `config.example.json` → `config.json`, заполните токен
   и режим API.
4. Запустите бинарь:
   - Linux: `./tinvest-gui-linux-amd64`
   - Windows: двойной клик по `tinvest-gui-windows-amd64.exe`

### Сборка из исходников

```
git clone https://github.com/svdmitrij/tinvest-snapshot.git
cd tinvest-snapshot
```

**Сборка под текущую платформу (Linux):**

```
CGO_ENABLED=1 go build -trimpath -o tinvest-gui ./cmd/gui
```

**Кросс-сборка (Linux + Windows) одной командой:**

```
./scripts/build-gui.sh          # обе платформы
./scripts/build-gui.sh linux    # только Linux
./scripts/build-gui.sh windows  # только Windows (нужен mingw-w64)
```

Результат — в `dist/`:
- `tinvest-gui-linux-amd64` — Linux (ELF 64-bit)
- `tinvest-gui-windows-amd64.exe` — Windows (PE32+)

### Установка CLI

CLI не требует графической среды — работает на серверах, в Cron,
на headless-машинах.

**Готовый бинарь:** скачайте `tinvest-snapshot-v<версия>-<платформа>-amd64`
из [релизов](https://github.com/svdmitrij/tinvest-snapshot/releases).

**Сборка из исходников:**

```
go build -o tinvest-snapshot ./cmd/snapshot
```

Кросс-сборка CLI:

```
./scripts/build.sh
```

Версионированные архивы для публикации релиза:

```
./scripts/package.sh v1.4.0
```

## Проверка работоспособности

### GUI

1. Создайте `config.json` с `mode: "prod"` и read-only токеном
   (через `token_env: "TINVEST_TOKEN"` и `export TINVEST_TOKEN=...`).
2. Запустите бинарь — должно открыться окно с четырьмя вкладками.
3. На вкладке «Портфель» нажмите «Обновить» — через несколько секунд
   таблица заполнится позициями.
4. Переключитесь на вкладку «Инструменты», выберите тип «Облигации»,
   нажмите «Поиск» — должен загрузиться справочник и отобразиться результаты.
5. На вкладке «Настройки» измените язык на `en` и нажмите «Сохранить» —
   интерфейс должен переключиться на английский без перезапуска.
6. Убедитесь, что токен не отображается: ни в интерфейсе настроек
   (поле токена — `PasswordEntry`, скрывает ввод), ни в экспортированных
   файлах.

### CLI

1. Установите переменную окружения: `export TINVEST_TOKEN=<токен>`.
2. Выполните: `./tinvest-snapshot --config config.json --mode prod`.
3. В консоли должна появиться сводка с числом счетов, позиций и операций.
4. Проверьте каталог `reports_dir` — созданы четыре файла:
   `portfolio_*.json`, `portfolio_*.csv`, `operations_*.csv`,
   `portfolio_*.xlsx`.
