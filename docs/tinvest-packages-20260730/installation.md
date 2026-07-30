# Установка tinvest-snapshot из нативных пакетов (deb, rpm, msi)

Начиная с версии 1.8.0 tinvest-snapshot распространяется не только в виде
tar.gz/zip-архивов, но и в формате нативных пакетов для основных ОС:

- **`.deb`** — Debian, Ubuntu и производные
- **`.rpm`** — RHEL, Fedora, CentOS, AlmaLinux и производные
- **`.msi`** — Windows 10+ (amd64)

Пакеты включают GUI-бинарь, CLI-бинарь, документацию, пример конфигурации,
иконку приложения и интеграцию в меню программ.

## Требования

### Linux (deb/rpm)

- X11 или Wayland (XWayland) — для GUI
- Библиотеки OpenGL: `libgl1` (Debian/Ubuntu) или `libglvnd-glx` (Fedora/RHEL)
- Библиотека X11: `libx11-6` (Debian/Ubuntu) или `libX11` (Fedora/RHEL)

Пакетный менеджер автоматически проверит зависимости при установке.
В desktop-дистрибутивах эти библиотеки, как правило, уже установлены.

### Windows (msi)

- Windows 10 или новее, amd64
- Права администратора для установки в `%ProgramFiles%`
- Сторонние рантаймы (Go, Fyne, .NET, Java) **не требуются** — бинари статические

## Установка

### Debian/Ubuntu — deb-пакет

Скачайте `tinvest-snapshot_<версия>_amd64.deb` из
[релизов](https://github.com/svdmitrij/tinvest-snapshot/releases) и установите:

```
sudo dpkg -i tinvest-snapshot_<версия>_amd64.deb
```

При отсутствии зависимостей (`libgl1`, `libx11-6`) dpkg сообщит об ошибке —
установите их и повторите:

```
sudo apt install -f
```

После установки:
- GUI запускается командой `tinvest-gui` или из меню приложений (раздел «Офис»)
- CLI запускается командой `tinvest-snapshot`

Проверьте, что пакет установлен:

```
dpkg -l tinvest-snapshot
```

### RHEL/Fedora — rpm-пакет

Скачайте `tinvest-snapshot-<версия>-1.x86_64.rpm` из
[релизов](https://github.com/svdmitrij/tinvest-snapshot/releases) и установите:

```
sudo rpm -i tinvest-snapshot-<версия>-1.x86_64.rpm
```

Или через dnf (автоматически установит зависимости):

```
sudo dnf install ./tinvest-snapshot-<версия>-1.x86_64.rpm
```

После установки:
- GUI запускается командой `tinvest-gui` или из меню приложений (раздел «Офис»)
- CLI запускается командой `tinvest-snapshot`

Проверьте, что пакет установлен:

```
rpm -q tinvest-snapshot
```

### Windows — msi-пакет

Скачайте `tinvest-snapshot-<версия>-amd64.msi` из
[релизов](https://github.com/svdmitrij/tinvest-snapshot/releases).

**Установка с GUI (рекомендуется):**

Дважды кликните по msi-файлу — запустится мастер установки:
экран приветствия, выбор пути установки (по умолчанию
`C:\Program Files\tinvest-snapshot\`), прогресс, завершение.

**Тихая установка:**

```
msiexec /i tinvest-snapshot-<версия>-amd64.msi /qn
```

После установки:
- GUI доступен через ярлык «T-Invest Snapshot» в меню «Пуск» → «Офис»
- CLI (`tinvest-snapshot.exe`) доступен из командной строки — путь установки
  добавляется в пользовательский `PATH`
- Приложение отображается в списке «Установка и удаление программ» с иконкой

## Состав пакета

### Linux (deb/rpm)

После установки на диске создаются:

| Путь | Назначение |
|---|---|
| `/opt/tinvest-snapshot/tinvest-gui` | GUI-бинарь |
| `/opt/tinvest-snapshot/tinvest-snapshot` | CLI-бинарь |
| `/opt/tinvest-snapshot/config.example.json` | Пример конфигурации |
| `/opt/tinvest-snapshot/README.md` | Документация |
| `/usr/local/bin/tinvest-gui` | Симлинк → GUI-бинарь (в PATH) |
| `/usr/local/bin/tinvest-snapshot` | Симлинк → CLI-бинарь (в PATH) |
| `/usr/share/applications/tinvest-snapshot.desktop` | Интеграция в меню приложений |
| `/usr/share/icons/hicolor/128x128/apps/tinvest-snapshot.png` | Иконка приложения |
| `/usr/share/doc/tinvest-snapshot/README.md` | Системная документация |
| `/usr/share/doc/tinvest-snapshot/changelog.gz` | Список изменений |
| `/usr/share/doc/tinvest-snapshot/copyright` | Лицензия |

### Windows (msi)

После установки на диске создаются:

| Путь | Назначение |
|---|---|
| `%ProgramFiles%\tinvest-snapshot\tinvest-gui.exe` | GUI-бинарь |
| `%ProgramFiles%\tinvest-snapshot\tinvest-snapshot.exe` | CLI-бинарь |
| `%ProgramFiles%\tinvest-snapshot\config.example.json` | Пример конфигурации |
| `%ProgramFiles%\tinvest-snapshot\README.md` | Документация |
| `%ProgramFiles%\tinvest-snapshot\icon.ico` | Иконка приложения |
| Меню «Пуск» → «Офис» → «T-Invest Snapshot» | Ярлык запуска GUI |
| `%ProgramFiles%\tinvest-snapshot\` в пользовательском `PATH` | Доступ к CLI из командной строки |

## Конфигурация после установки

Пакет устанавливает только `config.example.json`. Для работы скопируйте его
в `config.json` рядом с бинарём или в домашний каталог и заполните:

```
# Linux
cp /opt/tinvest-snapshot/config.example.json /opt/tinvest-snapshot/config.json
nano /opt/tinvest-snapshot/config.json
```

```
# Windows (PowerShell)
copy "C:\Program Files\tinvest-snapshot\config.example.json" "C:\Program Files\tinvest-snapshot\config.json"
notepad "C:\Program Files\tinvest-snapshot\config.json"
```

Либо используйте переменную окружения `TINVEST_TOKEN` (рекомендуется) вместо
поля `token` в конфиге — токен не попадёт в файл настроек. Подробнее —
[конфигурация](../configuration.md).

## Проверка работоспособности

### GUI (Linux)

1. Заполните конфиг (токен и режим `prod`).
2. Запустите GUI: `tinvest-gui` — должно открыться окно с вкладками
   «Портфель», «Операции», «Инструменты», «Настройки».
3. На вкладке «Портфель» нажмите «Обновить» — таблица заполнится позициями.
4. Проверьте, что приложение отображается в меню программ (раздел «Офис»)
   с иконкой в виде биржевого терминала.

### GUI (Windows)

1. Заполните конфиг (токен и режим `prod`).
2. Откройте меню «Пуск» → «Офис» → «T-Invest Snapshot».
3. Должно открыться окно с вкладками. На вкладке «Портфель» нажмите «Обновить».
4. Проверьте, что в «Установка и удаление программ» отображается
   «T-Invest Snapshot» с иконкой.

### CLI (Linux/Windows)

1. Убедитесь, что переменная `TINVEST_TOKEN` установлена.
2. Выполните: `tinvest-snapshot --mode prod`.
3. В консоли должна появиться сводка портфеля. Проверьте каталог отчётов.

## Удаление

### Linux

```
# Debian/Ubuntu
sudo dpkg -r tinvest-snapshot

# RHEL/Fedora
sudo rpm -e tinvest-snapshot
```

Удаление очищает все файлы пакета, включая симлинки, desktop-файл и иконку.
Пользовательский `config.json` и каталог отчётов (`reports_dir`) не удаляются —
их нужно удалить вручную при необходимости.

### Windows

```
msiexec /x tinvest-snapshot-<версия>-amd64.msi
```

Или через «Параметры» → «Приложения» → «Установленные приложения» →
«T-Invest Snapshot» → «Удалить».

Путь установки удаляется вместе с содержимым. Пользовательские данные
(`config.json`, отчёты) удаляются, если они внутри каталога установки;
файлы за его пределами не затрагиваются.

## Типовые проблемы

### `dpkg: зависимости не удовлетворены`

Установите недостающие библиотеки:

```
sudo apt install libgl1 libx11-6
```

И повторите `sudo dpkg -i ...`.

### `rpm: Failed dependencies`

Установите зависимости перед пакетом:

```
sudo dnf install libglvnd-glx libX11
```

Или используйте `dnf install ./пакет.rpm` — dnf предложит установить
зависимости автоматически.

### Приложение не появляется в меню (Linux)

Выполните выход из сессии и повторный вход — desktop-окружение кэширует
desktop-файлы и может не подхватить изменения сразу. Альтернативно:

```
update-desktop-database ~/.local/share/applications/  # для некоторых DE
```

### GUI не запускается: «cannot open display»

Нет графической сессии. Используйте CLI (`tinvest-snapshot`), который
работает без GUI. Или запустите GUI в X11/Wayland-сессии.

### Windows: «Windows защитила ваш компьютер»

SmartScreen может показать предупреждение при первом запуске msi —
нажмите «Подробнее» → «Выполнить в любом случае». Это происходит
потому что пакет не подписан сертификатом (подпись msi не входит
в текущий объём работ).

### Windows: CLI не доступен в командной строке

Путь `%ProgramFiles%\tinvest-snapshot\` добавляется в пользовательский
`PATH`. Если команда не найдена — перезапустите терминал: переменные
окружения обновляются при новом входе в сессию.
