---
title: Установка
description: Пакеты для RHEL, Fedora, Debian и Ubuntu, единый бинарный файл, Docker в Windows и то, как ducktainer подключается к Docker.
order: 2
---

## Пакеты

Пакеты — рекомендуемый способ. Они работают на RHEL/Alma/Rocky 9+, Fedora, Debian 12+ и Ubuntu 22.04+, на amd64 и arm64. Docker должен быть уже установлен, так как пакет от него зависит.

Скачайте пакет для своей системы и установите его (все файлы есть на [странице релизов](https://github.com/leandrofus/ducktainer/releases/latest)):

```sh
# RHEL / Fedora
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest.x86_64.rpm
sudo dnf install ./ducktainer-latest.x86_64.rpm

# Debian / Ubuntu
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer_latest_amd64.deb
sudo apt install ./ducktainer_latest_amd64.deb
```

На arm64 используйте `ducktainer-latest.aarch64.rpm` или `ducktainer_latest_arm64.deb`. Эти ссылки всегда указывают на последний релиз.

Пакет:

- создаёт пользователя `ducktainer` и группы `ducktainer-admins` / `ducktainer-viewers`;
- запускает `ducktainer setup`, чтобы определить, как установлен Docker;
- включает службу `ducktainer`, которая слушает `http://127.0.0.1:9900`.

## Windows (MSI)

В PowerShell от имени администратора:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

Установщик:

- помещает `ducktainer.exe` в `C:\Program Files\ducktainer` и добавляет его в PATH;
- регистрирует службу `ducktainer` (`ducktainer serve` от имени LocalSystem, перезапускается при сбое), слушающую на `http://127.0.0.1:9900`, и пункт в меню «Пуск»;
- хранит данные в `C:\ProgramData\ducktainer`, доступном только SYSTEM и администраторам. В терминале администратора `ducktainer token`, `ducktainer allow` и `ducktainer auth` работают с конфигурацией службы.

Вход выполняется под учётными записями Windows (локальными или `ДОМЕН\пользователь`). Члены групп «Администраторы», `docker-users` и `ducktainer-admins` — администраторы; члены `ducktainer-viewers` получают доступ только для чтения. Роли определяются локальными группами, поэтому доменная группа учитывается, если она входит в одну из них. При обновлении конфигурация сохраняется, а при удалении `C:\ProgramData\ducktainer` остаётся на месте. Если Docker на машине ещё нет, установите его командой `ducktainer engine install` (ниже).

## Единый бинарный файл

Бинарный файл работает в любой системе Linux, macOS или Windows. Без пакета PAM недоступен, поэтому в Linux и macOS вход выполняется по токену, который выводится при запуске. В Windows бинарный файл также принимает учётные записи Windows.

```sh
./ducktainer serve            # http://127.0.0.1:9900, выводит токен доступа
./ducktainer ui               # то же самое, и открывает браузер
```

## Windows без Docker Desktop

`ducktainer engine` устанавливает за вас Docker Engine с открытым исходным кодом. Запустите его из терминала с правами администратора:

```powershell
.\ducktainer.exe engine install                 # Linux-контейнеры: Docker в собственном дистрибутиве WSL 2
.\ducktainer.exe engine install --mode native   # Windows-контейнеры: dockerd.exe как служба Windows
.\ducktainer.exe engine status
.\ducktainer.exe ui
```

- **`wsl`** (по умолчанию) создаёт дистрибутив WSL 2 `ducktainer` из образа Ubuntu, проверяя его по SHA256SUMS Ubuntu. Внутри он запускает `docker.io` с systemd и добавляет хост `wsl` (`wsl://ducktainer`). При входе в систему задача запускает `ducktainer engine relay` для пользователя, выполнившего установку. Ретранслятор не даёт дистрибутиву остановиться, поэтому контейнеры не останавливаются, когда WSL стал бы его выключать. Он также предоставляет Docker дистрибутива через именованный канал, потому что дистрибутивы WSL принадлежат пользователю, а служба (LocalSystem) не может их запускать. Служба добавляет хост `wsl`, когда находит ретранслятор администратора, и эти контейнеры работают, пока этот пользователь находится в системе.
- **`native`** включает компонент Containers (и Hyper-V в Windows 10/11), устанавливает `dockerd.exe`, `docker.exe` и плагин compose в `C:\Program Files\Docker` и регистрирует службу `docker`. Хост `local` затем указывает на `npipe:////./pipe/docker_engine`. В Windows Home компонента Containers нет: используйте `wsl`.
- Если Windows требует перезагрузки для включения WSL или компонента, перезагрузите систему и выполните ту же команду снова.

## Проверка, как ducktainer подключается к Docker

```sh
sudo ducktainer setup --dry-run
```

```
socket:  /var/run/docker.sock  (root:docker 0660, standard path)
mode:    group
  · access through group docker (socket root:docker 0660)
```

| Обнаруженная установка | Служба запускается от имени |
|---|---|
| Сокет доступен группе (`root:docker 0660`, любое имя группы) | `ducktainer` + эта группа |
| Rootless Docker (`/run/user/1000/docker.sock`) или rootless Podman (`/run/user/1000/podman/podman.sock`) | владелец сокета |
| Сокет только для root, Podman | root (с предупреждением) |
| Установлен, но ещё не запущен | то, что указано в юните `docker.socket`; Podman без активного сокета API: `podman.socket` включается сам (root) |

Запускайте `sudo ducktainer setup` снова всякий раз, когда меняется установка Docker.
