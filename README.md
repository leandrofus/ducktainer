<p align="center"><img src="assets/brand/ducktainer-logo.svg" alt="ducktainer: containerized applications with a squeak" width="420"></p>

# ducktainer

[![CI](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml/badge.svg)](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml)

**Docker management in a single binary.** A CRM-style web UI (right-click everything, saved views, kanban, ⌘K palette), compose stacks with a visual editor, an app marketplace, live logs and web terminals, one-click access to every container port, private tunnels and Cloudflare publishing. Sign-in uses your **system users** (PAM), and network access is limited to the hosts you allow.

[Español](README.es.md) · [Русский](README.ru.md) · [ducktainer website](https://leandrofus.github.io/ducktainer/)

- **Nothing to install on remote hosts**: they are reached over **SSH** (`docker system dial-stdio`, like `DOCKER_HOST=ssh://`), over TLS (`tcp://`, e.g. a remote `ducktainer expose`), or, for nodes behind NAT or a firewall, through an optional **outbound agent** (the same binary) that connects back to the server.
- **Least privilege**: the service runs as an unprivileged user. `ducktainer setup` adapts it to however Docker is installed: socket group, rootless or root-only. Only password checks run as root, in a socket-activated helper.
- **Access control**: an IP/CIDR/hostname allowlist (connections are dropped before TLS), roles from POSIX groups, automatic TLS with certificate pinning, and an audit log.
- **Languages**: English, Spanish and Russian. The UI follows the browser and can be switched from the user menu; the CLI follows `LANG`.

---

## Getting started

### 1. Install

**Packages (recommended)**: RHEL/Alma/Rocky 9+, Fedora, Debian 12+, Ubuntu 22.04+ (amd64 and arm64). The package depends on a Docker-compatible engine (`docker-ce`, `moby-engine`, `docker.io`, `podman-docker`…) and recommends the compose plugin and the SSH client.

```sh
# RHEL / Fedora
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest.x86_64.rpm
sudo dnf install ./ducktainer-latest.x86_64.rpm

# Debian / Ubuntu
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer_latest_amd64.deb
sudo apt install ./ducktainer_latest_amd64.deb
```

On arm64 use `ducktainer-latest.aarch64.rpm` or `ducktainer_latest_arm64.deb`. These links always point to the latest release.

The package:

- creates the `ducktainer` user and the `ducktainer-admins` / `ducktainer-viewers` groups;
- creates `/etc/ducktainer/secret.key`, the key that encrypts the secrets in the configuration;
- runs `ducktainer setup` to detect how Docker is installed (and, on machines with a desktop, prepares the start menu);
- enables `ducktainer.service` and the `ducktainer-pam.socket` helper, listening on `http://127.0.0.1:9900`.

Upgrades keep your configuration and only restart the service if it was running.

**Windows (MSI)**, from an administrator PowerShell:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

The installer:

- puts `ducktainer.exe` in `C:\Program Files\ducktainer` and on the PATH;
- registers the `ducktainer` service (`ducktainer serve` as LocalSystem, restarted if it fails), listening on `http://127.0.0.1:9900`, and a Start menu entry;
- keeps the data in `C:\ProgramData\ducktainer`, which only SYSTEM and the Administrators can read. From an administrator terminal, `ducktainer token`, `ducktainer allow` or `ducktainer auth` work on the service's configuration.

People sign in with their Windows accounts (local, or `DOMAIN\user`). Members of Administrators, `docker-users` or `ducktainer-admins` are administrators; members of `ducktainer-viewers` get read-only access. Roles come from local groups, so a domain group counts once it is a member of one of them. Upgrades keep the configuration, and uninstalling leaves `C:\ProgramData\ducktainer` in place. If the machine has no Docker yet, install it with `ducktainer engine install` (below).

**Single binary** (any Linux, macOS or Windows; without PAM, Linux and macOS only offer token sign-in, while Windows also signs in Windows accounts):

```sh
./ducktainer serve            # http://127.0.0.1:9900, prints the access token
./ducktainer ui               # same, and opens the browser
```

Without the package, data lives in the user's configuration directory (`~/.config/ducktainer` on Linux) or wherever `DUCKTAINER_HOME` points; `ducktainer help` prints the exact path.

**Windows without Docker Desktop.** `ducktainer engine` installs the open-source Docker Engine for you, from an administrator terminal:

```powershell
.\ducktainer.exe engine install                 # Linux containers: Docker in a WSL 2 distribution of its own
.\ducktainer.exe engine install --mode native   # Windows containers: dockerd.exe as a Windows service
.\ducktainer.exe engine status
.\ducktainer.exe ui
```

- `wsl` (the default) creates the `ducktainer` WSL 2 distribution from Ubuntu's image (checked against Ubuntu's SHA256SUMS), runs `docker.io` there with systemd and adds the `wsl` host (`wsl://ducktainer`). At logon, a task starts `ducktainer engine relay` for the user who ran the install. The relay keeps the distribution running, so containers don't stop when WSL would shut it down. It also serves the distribution's Docker on a named pipe, because WSL distributions belong to a user and the service (LocalSystem) can't start them. The service adds the `wsl` host when it finds an administrator's relay, and those containers run while that user is signed in (`--no-keepalive` skips the task).
- `native` enables the Containers feature (and Hyper-V on Windows 10/11), installs `dockerd.exe`, `docker.exe` and the compose plugin in `C:\Program Files\Docker`, registers the `docker` service and points the `local` host at `npipe:////./pipe/docker_engine`. `--version` and `--sha256` pin the Docker release. Windows Home has no Containers feature: use `wsl`.
- If Windows needs a restart (to enable WSL or a feature), restart and run the same command again.

### 2. Check how ducktainer reaches Docker

```sh
sudo ducktainer setup --dry-run
```

```
socket:  /var/run/docker.sock  (root:docker 0660, standard path)
mode:    group
  · access through group docker (socket root:docker 0660)
```

| Detected installation | The service runs as |
|---|---|
| Socket accessible by a group (`root:docker 0660`, any group name) | `ducktainer` + that group |
| Rootless Docker (`/run/user/1000/docker.sock`) or rootless Podman (`/run/user/1000/podman/podman.sock`) | the socket owner |
| Root-only socket, Podman | root (with a warning) |
| Installed but not started yet | what the `docker.socket` unit says; Podman without an active API socket: `podman.socket` is enabled for you (root) |

The result is written to `/etc/systemd/system/ducktainer.service.d/10-docker-access.conf`. Run `sudo ducktainer setup` again whenever the Docker installation changes (`--socket PATH` forces a socket).

### 3. Give people access

Accounts are ordinary system users, local or from LDAP/SSSD/FreeIPA. Their **role** depends on their groups:

| Role | Groups (default) | Can |
|---|---|---|
| **Administrator** | `ducktainer-admins`, `docker` | everything: containers, stacks, images, terminals, tunnels, settings, users |
| **Read-only** | `ducktainer-viewers` | see containers, stacks, images, networks, volumes and hosts; cannot change anything, open terminals, browse or download files, or see other settings. Passwords in connection strings are hidden |

```sh
sudo useradd -m ana && sudo passwd ana       # only if the account doesn't exist yet
sudo usermod -aG ducktainer-admins ana          # administrator  (or ducktainer-viewers: read-only)
sudo ducktainer auth test ana                   # checks the password and shows the resulting role
```

- Group membership is re-checked every 5 minutes, so removing someone from the group revokes their access quickly.
- The groups can be changed, and single users added, with `ducktainer auth set --admin-groups … --viewer-groups … --admin-users … --viewer-users …`. Sessions last 12 hours (`--session-hours`).
- `root` cannot sign in unless you enable it (`ducktainer auth set --allow-root on`).
- 5 failed attempts within 10 minutes block further tries until the oldest one is 10 minutes old.
- The UI shows all of this under **Settings → Access → People**: the role → group mapping, the current members of each group, and a guide that writes these commands for you.

### 4. Open it to the network

By default ducktainer only listens on the local machine. Allow the addresses that may connect (IPs, CIDR ranges or host names; host names are re-resolved every minute, so dynamic DNS works) and restart:

```sh
sudo ducktainer allow add 192.168.1.0/24 office.example.com
sudo systemctl restart ducktainer       # now it listens on 0.0.0.0:9900, with TLS
```

- Anyone else is disconnected before TLS. The local machine is always allowed.
- **Settings → Access → Network** shows the listen address, your own IP and the rule that lets it in. You can edit the allowlist there; changes apply immediately. There is also a "would this address be allowed?" checker.
- The TLS certificate is self-signed. Its SHA-256 fingerprint is printed at startup so clients can pin it. Use your own certificate with `--cert/--key` in `DUCKTAINER_OPTS` (`/etc/ducktainer/ducktainer.env`); `--tls on|off` overrides the automatic choice.
- Private tunnels (see [Features](#features)) use ports `41000-41099` of the server (`--tunnel-ports`): open them in the firewall if you want to use them.

### 5. Sign in

Open `https://<server>:9900` and sign in with your system user and password.

The **emergency token** signs in as administrator and authorizes the API (`Authorization: Bearer`). Only its hash is stored, so it is shown once: `sudo ducktainer token --rotate` creates and prints a new one. To allow only system users, disable it with `sudo ducktainer auth set --token-login off`. **Settings → Access → Sign-in** has the same switches.

### 6. Add more Docker hosts (optional)

```sh
# using the outbound agent (ideal for nodes behind NAT/firewall): add the host
# first (Settings → Hosts → Agent (outbound / Edge), or the line below), which gives it a token of
# its own, then run on the node the command shown in "Copy agent install command":
ducktainer endpoint add worker-1 agent://          # prints the host's token
#   curl -fsSL -k --pinnedpubkey 'sha256//…' -H 'Authorization: Bearer <host token>' \
#     'https://<ducktainer-server>:9900/api/agent/install.sh?server=…' | sudo sh

# over SSH: nothing to install on the remote (key-based access; the user must be allowed to use Docker)
sudo -u ducktainer ssh-keygen -t ed25519          # key in /var/lib/ducktainer/.ssh, add it to the remote's authorized_keys
ducktainer endpoint add prod ssh://deploy@prod.example.com      # --key FILE to use another key

# or through the proxy, on the remote host:
ducktainer expose --allow <ducktainer-server-ip>   # prints the exact `ducktainer endpoint add …` line to run here
```

Hosts can also be managed from **Settings → Hosts**, and you switch between them from the top of the sidebar.

- Each agent host has its own token, valid only for that host; the admin token is not accepted from agents. The install command sends it in a header, pins this server's certificate (no `--insecure`), installs the agent as a systemd service and leaves the token on the node in `/etc/ducktainer-agent/agent.env` (root only). Hosts registered by earlier versions with the admin token are refused until you give them a token in Settings → Hosts and reinstall the agent.
- If agents can't reach the allowlisted port, `serve --agent-listen :9901` opens a port only for agents (token required, no IP allowlist).
- Agents keep themselves at the server's version: when it changes they download the new binary, check that it runs on the node and restart into it, keeping the current one if anything fails (turn it off per host in Settings → Hosts; builds marked `dev` and Windows never update themselves). The packages carry static agent binaries for amd64 and arm64, so nodes need no particular glibc (RHEL 8 works) and can be of another architecture than the server. Agents installed before this version must be reinstalled once.
- `tcp://` hosts must use TLS unless they are on the same machine; without `--fingerprint`, the certificate presented when the host is added is pinned.
- SSH hosts record the host key on the first connection (`StrictHostKeyChecking=accept-new`); a changed key is refused.
- Registry credentials can be limited to some hosts (`--hosts`, or in the UI): compose runs on a node get a copy of the credentials they are allowed to see.

#### Trust model

ducktainer controls the Docker API of every host, which is equivalent to root on them: whoever controls the ducktainer server (or its admin accounts) controls all hosts. Keep that machine and `/etc/ducktainer/secret.key` as protected as the hosts themselves. To edit stacks on a node, ducktainer starts a helper container there (`ducktainer-files`) that mounts the node's `/`; it exits after an hour idle.

---

## Features

- **Jump** (the start page of each host): every port of every container, split into web ports (one click opens them) and TCP/UDP ports. For known services (PostgreSQL, MySQL/MariaDB, MongoDB, Redis, RabbitMQ) it builds the connection string with the credentials found in the compose environment. Compose labels override the guesswork: `ducktainer.url=https://app.example.com`, `ducktainer.url.<port>=…` and `ducktainer.kind.<port>=http|tcp`.
- **Private tunnels**: right-click a port and choose "Open a tunnel…" to reach it through the ducktainer server, also unpublished ports and ports on hosts behind an agent or NAT. A tunnel closes after 30 minutes without connections, at a fixed time, or stays open (also across restarts) until you close it. It accepts only the address that opened it, or every address allowed to reach ducktainer.
- **Public exposure through Cloudflare**: "Expose to the internet…" on a web port publishes it with `cloudflared` (downloaded and checksum-verified if it isn't installed). Without an account you get a random `trycloudflare.com` address; with an API token (Settings → Cloudflare) each service gets a fixed name on your own domains. Access can be public or limited to some IPs/hosts, to a ducktainer sign-in (optionally some users or groups), or to whoever has a secret link, and it can expire.
- **Start menu**: on a machine with a desktop, the web apps of its containers can be added to a "Containers" menu (Linux, freedesktop entries) or to Start › Programs › Containers (Windows), one by one or automatically; the menu refreshes on its own (on Linux, through a systemd user unit that `ducktainer setup` installs for every user). **Settings → Access → Start menu**.
- **Containers**: a table grouped by stack, or a kanban by state (drag a card to "Stopped" to stop it), saved views, bulk actions and quick view. Each record has Overview (live CPU/memory/network plus activity), Logs, Terminal, Files, Performance, Processes, Configuration and Inspect.
- **Editing containers**: Docker can't modify a container in place, so ducktainer creates a new one with the edited settings, swaps the names and removes the old one. Volumes are kept, and the original is restored if the new one fails to start. Containers can also be renamed, duplicated, updated (pull + recreate) and committed to an image.
- **Compose stacks**:
  - a **visual editor** (YAML stays the source of truth: comments, formatting and variables are preserved) and a YAML editor;
  - multi-file projects (base + overrides) with a merged view and "merge into one file";
  - validation before saving (`docker compose config`), deploy and update, combined logs per service;
  - stacks created outside ducktainer are detected and edited in place, or adopted into ducktainer;
  - a stack can live in ducktainer's store or in a directory of your choice, also on remote nodes.
- **Swarm mode**: when a host is part of a swarm, a Swarm group appears in the sidebar:
  - **Cluster**: initialize a swarm or join one, join commands for workers and managers (with token rotation), leave the swarm;
  - **Services**: create (image, replicas or global, ports, environment, overlay networks, secrets, configs, constraints, limits), scale, restart, update the image, roll back, edit the full spec, tasks per node and live logs;
  - **Swarm stacks** deployed with `docker stack deploy` from a compose file and `.env` (same editors as compose stacks). Like compose stacks they live in ducktainer's store or in a directory of their own, also on remote nodes; services with `build:` (and an `image:` to name the result) are built there before deploying, and optionally pushed to their registry so every node can pull them. Stacks deployed elsewhere are listed and can be adopted by pasting their compose or pointing at its directory. Swarm templates from the marketplace deploy here;
  - **Nodes**: availability (active, pause, drain), promote/demote, labels, tasks per node, remove;
  - **Secrets and configs**: create from text or a file, see which services use them.
  Workers only show the cluster page: the swarm is managed from a manager host.
- **Images**: pull and push with per-layer progress, tag, build from a Dockerfile, layer history, cleanup.
- **Networks**: a registry with connected containers and their IPs, a connection map, create, connect and disconnect.
- **Volumes**: who uses each one, orphans, sizes, and browsing their contents through a running container that mounts them.
- **Marketplace**: 26 built-in apps plus any Portainer v2/v3 template list. Portainer's official list is enabled by default; the community list (~800 apps, Lissy93) is preconfigured but off. Manage sources in Settings → Marketplace.
- **Dashboard and activity**: host summary, disk usage and cleanup, and the live Docker event stream.
- **Audit log**: every sign-in and every write or terminal action, in Settings → Audit and in `journalctl -u ducktainer`.

## Command line

| Command | Purpose |
|---|---|
| `ducktainer serve` | Web UI + API (`--listen`, `--allow`, `--tls auto\|on\|off`, `--cert/--key`, `--expose :2376 [--expose-read-only] [--expose-no-exec]`, `--agent-listen :9901`, `--tunnel-ports 41000-41099`) |
| `ducktainer ui` | Like `serve` on 127.0.0.1, and opens the browser |
| `ducktainer agent` | Outbound agent (`--server`, `--name`, `--fingerprint`, `--socket`, `--install-service`; token in `DUCKTAINER_AGENT_TOKEN`) |
| `ducktainer expose` | Publish only the Docker API, protected (allowlist + token + TLS; `--read-only`, `--no-exec`) |
| `ducktainer engine install [--mode wsl\|native] \| status \| relay` | Windows: install Docker without Docker Desktop |
| `ducktainer setup [--dry-run] [--socket PATH]` | Adapt the service to the Docker installation |
| `ducktainer install-service [serve flags]` | Install a systemd service for the single binary (root) |
| `ducktainer auth show \| set … \| test <user>` | System-user sign-in and role mapping |
| `ducktainer allow ls \| add \| rm` | Network allowlist |
| `ducktainer token [--rotate]` | Create and print a new emergency/API token (only its hash is stored, so without `--rotate` it is shown only right after it was created) |
| `ducktainer endpoint ls \| add \| rm` | Docker hosts (`unix://`, `ssh://user@host`, `tcp://host:2376`, `agent://`, `wsl://distro`) |
| `ducktainer registry ls \| add \| rm` | Private registry credentials (password on stdin; `--hosts` limits which hosts get them) |
| `ducktainer ps \| logs \| sh` | Containers by stack · logs of a container or a whole stack · interactive shell (picks bash/ash/sh); `-e <host>` picks the host |
| `ducktainer docker-env <host>` | `eval "$(ducktainer docker-env prod)"` to use the docker CLI against a host |
| `ducktainer version` | Print the version |

## Files

| Path | Contents |
|---|---|
| `/etc/ducktainer/ducktainer.env` | `DUCKTAINER_OPTS` for `ducktainer serve` |
| `/etc/ducktainer/secret.key` | key that encrypts the secrets in the configuration (root-only, passed to the service with systemd `LoadCredential`) |
| `/etc/pam.d/ducktainer` | PAM stack for sign-in (`password-auth` on RHEL, `common-auth` on Debian) |
| `/var/lib/ducktainer/` | `config.json`, stacks (`stacks/<host>/<name>`), audit log, TLS certificate, the service's SSH keys, the downloaded `cloudflared` |
| `/etc/systemd/system/ducktainer.service.d/10-docker-access.conf` | written by `ducktainer setup` |
| `/usr/local/share/applications/ducktainer-containers/` | start menu entries (machines with a desktop) |

Secrets in `config.json` (session secret, host tokens, registry passwords, the Cloudflare tokens, secret links) are encrypted with AES-256-GCM, and the emergency token is stored only as a SHA-256 hash. The key is kept apart from the data, so a copy or backup of `/var/lib/ducktainer` reveals nothing without it. Back up `/etc/ducktainer/secret.key` separately: without it the configuration cannot be read. For a binary run without the package, the key is `secret.key` in the data directory, unless `DUCKTAINER_KEY_FILE` points elsewhere. Plaintext configurations from earlier versions are encrypted on first start.

## Translations

English is the source language. To add a language:

1. Copy `web/src/i18n/es.ts` (UI) and `internal/i18n/es.go` (API errors and CLI). Keys are the English strings; `ctx|text` keys are gender/number variants used by `tc()`.
2. Register the new catalog in `web/src/i18n/index.ts` and `internal/i18n/i18n.go`.

## Development

```sh
make help           # show all available make targets
make run            # build and run ducktainer serve locally (http://127.0.0.1:9900; ARGS="..." for flags)
make ui             # build and run ducktainer ui (opens browser)
make build          # web UI + static binary (no PAM) in bin/
make build-pam      # with PAM (needs gcc and pam-devel / libpam0g-dev)
make test           # Go unit tests + web typecheck (test-unit, test-race, test-web separately)
make dev            # UI with hot reload against a running `ducktainer serve` on :9900
make packages       # rpm + deb (amd64, arm64) built in a container; only Docker needed
make dist           # static binaries for linux/darwin/windows
make clean          # remove build artifacts
```

Layout:

- `main.go`: CLI.
- `internal/server`: HTTP/WebSocket API, jump screen, tunnels, Cloudflare exposures, start menu sync.
- `internal/docker`: Engine API client (unix, ssh, tcp+tls, npipe, agent).
- `internal/agent`: outbound agent and its tunnel.
- `internal/auth`: PAM, roles and the root helper.
- `internal/setup`: Docker install detection.
- `internal/engine`: Docker installation on Windows (WSL 2 / native).
- `internal/cloudflare`: `cloudflared` and the Cloudflare API.
- `internal/shortcuts`: start menu entries (Linux, Windows).
- `internal/compose`, `internal/templates`, `internal/proxy`, `internal/access`, `internal/config`, `internal/probe`, `internal/tlsutil`, `internal/cli`, `internal/i18n`.
- `packaging/`: systemd units, PAM files, nfpm.
- `web/`: React + Vite.
- `site/`: the website (Astro).
