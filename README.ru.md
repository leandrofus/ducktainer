<p align="center"><img src="assets/brand/ducktainer-logo.svg" alt="ducktainer: контейнеризированные приложения, с писком" width="420"></p>

# ducktainer

[![CI](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml/badge.svg)](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml)
[![Версия](https://img.shields.io/github/v/release/leandrofus/ducktainer)](https://github.com/leandrofus/ducktainer/releases)
[![Лицензия](https://img.shields.io/github/license/leandrofus/ducktainer)](LICENSE)
[![Версия Go](https://img.shields.io/github/go-mod/go-version/leandrofus/ducktainer)](go.mod)

Управление Docker в одном бинарном файле. ducktainer даёт веб-интерфейс для контейнеров, стеков compose, образов, сетей и томов, с логами и терминалами в реальном времени, каталогом приложений и приватными туннелями. Вход выполняется под вашими системными пользователями, и подключаться могут только разрешённые адреса.

[Документация](https://leandrofus.github.io/ducktainer/ru/docs/) · [Discord](https://discord.gg/H28S6chSy) · [English](README.md) · [Español](README.es.md) · [Релизы](https://github.com/leandrofus/ducktainer/releases)

## Возможности

- **Контейнеры**: представления в виде таблицы или доски, CPU, память и сеть в реальном времени, логи, терминалы, файлы и процессы. Изменение контейнера пересоздаёт его безопасно и восстанавливает исходный, если новый не запустился.
- **Стеки compose**: визуальный редактор, который сохраняет ваш YAML, комментарии и переменные; проекты из нескольких файлов, проверка перед сохранением и объединённые логи.
- **Образы, сети и тома**: загрузка и отправка с прогрессом по слоям, сборка из Dockerfile, просмотр того, какие контейнеры используют каждую сеть и том, и очистка всего, что никем не используется.
- **Каталог приложений**: встроенные приложения и любые списки шаблонов Portainer, развёртываемые как редактируемый стек.
- **Swarm**: управление кластером, сервисами, узлами, секретами, конфигами и стеками swarm.
- **Удалённые хосты**: доступ к другим машинам по SSH, по TLS или через исходящий агент для хостов за NAT. При SSH на удалённом хосте ничего устанавливать не нужно.
- **Доступ**: вход под учётными записями Linux или Windows. Роли берутся из системных групп, а список разрешённых адресов отсекает неизвестные подключения до TLS.
- **Журнал аудита**: каждый вход, изменение и сеанс терминала записываются.

Веб-интерфейс и командная строка доступны на английском, испанском и русском языках.

## Установка

### Пакеты Linux

Пакеты есть для RHEL, Alma, Rocky и Fedora (rpm), а также для Debian и Ubuntu (deb), для amd64 и arm64. Docker или совместимый движок должен быть уже установлен.

```sh
# RHEL / Fedora
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest.x86_64.rpm
sudo dnf install ./ducktainer-latest.x86_64.rpm

# Debian / Ubuntu
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer_latest_amd64.deb
sudo apt install ./ducktainer_latest_amd64.deb
```

Пакет запускает службу `ducktainer` на `http://127.0.0.1:9900`. Он также определяет, как установлен Docker, чтобы служба могла обращаться к сокету.

### Windows

В PowerShell от имени администратора:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

Установщик регистрирует службу `ducktainer` на `http://127.0.0.1:9900`. Docker Desktop не нужен: `ducktainer engine install` устанавливает движок Docker с открытым исходным кодом.

### Один бинарный файл

Скачайте бинарный файл для своей платформы со [страницы релизов](https://github.com/leandrofus/ducktainer/releases) или соберите его сами (см. [Разработка](#разработка)).

```sh
./ducktainer serve     # http://127.0.0.1:9900, выводит токен доступа
./ducktainer ui        # то же самое и открывает браузер
```

Без пакетов Linux вход выполняется по токену доступа, потому что статический бинарный файл не включает PAM. Бинарные файлы для Windows также принимают учётные записи Windows.

## Первые шаги

1. **Проверьте, как ducktainer обращается к Docker**: `sudo ducktainer setup --dry-run` показывает сокет и пользователя службы, которые будут использованы. Если установка Docker изменилась, запустите `sudo ducktainer setup`.
2. **Дайте людям доступ**: добавьте их системного пользователя в `ducktainer-admins` (полный доступ) или в `ducktainer-viewers` (только чтение).
3. **Откройте доступ к сети**: по умолчанию ducktainer слушает только локальную машину. Разрешите адреса, которые могут подключаться, и перезапустите службу:

   ```sh
   sudo ducktainer allow add 192.168.1.0/24
   sudo systemctl restart ducktainer
   ```

4. **Войдите** по адресу `https://<сервер>:9900`. Отпечаток самоподписанного сертификата выводится при запуске, чтобы клиенты могли его закрепить.

[Документация](https://leandrofus.github.io/ducktainer/ru/docs/) подробно описывает каждый шаг, включая удалённые хосты, публикацию через Cloudflare, командную строку и то, где ducktainer хранит свои файлы.

## Безопасность

- Служба работает от непривилегированного пользователя. Только проверка паролей выполняется от root, во вспомогательном процессе, который запускается через сокет.
- Секреты в конфигурации шифруются AES-256-GCM, ключ хранится вне каталога данных. Сохраните `/etc/ducktainer/secret.key` отдельно.
- Аварийный токен доступа хранится только в виде хеша.

ducktainer управляет API Docker каждого управляемого хоста, что равнозначно root на этих хостах. Защищайте сервер ducktainer и его учётные записи администраторов так же, как сами хосты.

## Разработка

```sh
make help      # все цели make
make run       # сборка и запуск ducktainer serve на http://127.0.0.1:9900
make test      # тесты Go и проверка типов веб-интерфейса
```

Структура кода:

- `main.go`: точка входа командной строки.
- `internal/`: пакеты Go (сервер, клиент Docker, агент, аутентификация, compose и другие).
- `web/`: веб-интерфейс (React и Vite).
- `site/`: сайт проекта (Astro).
- `packaging/`: юниты systemd, файлы PAM и описания пакетов.

Как сообщить о проблеме, настроить проект, запустить проверки и отправить изменение, описано в [CONTRIBUTING.md](CONTRIBUTING.md).

## Лицензия

ducktainer — программа со свободным исходным кодом, распространяется по **GNU Affero General Public License, версия 3 или (по вашему выбору) любая более поздняя** (`AGPL-3.0-or-later`). Полный текст — в [LICENSE](LICENSE).
