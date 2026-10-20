---
title: Install
description: Packages for RHEL, Fedora, Debian and Ubuntu, the single binary, Docker on Windows, and how ducktainer reaches Docker.
order: 2
---

## Packages

The packages are the recommended way. They work on RHEL/Alma/Rocky 9+, Fedora, Debian 12+ and Ubuntu 22.04+, on amd64 and arm64. Docker must already be installed, because the package depends on it.

Download the package for your system and install it (all files are on the [releases page](https://github.com/leandrofus/ducktainer/releases/latest)):

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
- runs `ducktainer setup` to detect how Docker is installed;
- enables the `ducktainer` service, listening on `http://127.0.0.1:9900`.

## Windows (MSI)

From an administrator PowerShell:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

The installer:

- puts `ducktainer.exe` in `C:\Program Files\ducktainer` and on the PATH;
- registers the `ducktainer` service (`ducktainer serve` as LocalSystem, restarted if it fails), listening on `http://127.0.0.1:9900`, and a Start menu entry;
- keeps the data in `C:\ProgramData\ducktainer`, which only SYSTEM and the Administrators can read. From an administrator terminal, `ducktainer token`, `ducktainer allow` or `ducktainer auth` work on the service's configuration.

People sign in with their Windows accounts (local, or `DOMAIN\user`). Members of Administrators, `docker-users` or `ducktainer-admins` are administrators; members of `ducktainer-viewers` get read-only access. Roles come from local groups, so a domain group counts once it is a member of one of them. Upgrades keep the configuration, and uninstalling leaves `C:\ProgramData\ducktainer` in place. If the machine has no Docker yet, install it with `ducktainer engine install` (below).

## Single binary

The binary runs on any Linux, macOS or Windows. Without the package there is no PAM, so on Linux and macOS you sign in with the token it prints at startup. On Windows the binary also signs in Windows accounts.

```sh
./ducktainer serve            # http://127.0.0.1:9900, prints the access token
./ducktainer ui               # same, and opens the browser
```

## Windows without Docker Desktop

`ducktainer engine` installs the open-source Docker Engine for you. Run it from an administrator terminal:

```powershell
.\ducktainer.exe engine install                 # Linux containers: Docker in a WSL 2 distribution of its own
.\ducktainer.exe engine install --mode native   # Windows containers: dockerd.exe as a Windows service
.\ducktainer.exe engine status
.\ducktainer.exe ui
```

- **`wsl`** (the default) creates the `ducktainer` WSL 2 distribution from Ubuntu's image, checked against Ubuntu's SHA256SUMS. It runs `docker.io` there with systemd and adds the `wsl` host (`wsl://ducktainer`). At logon, a task starts `ducktainer engine relay` for the user who ran the install. The relay keeps the distribution running, so containers don't stop when WSL would shut it down. It also serves the distribution's Docker on a named pipe, because WSL distributions belong to a user and the service (LocalSystem) can't start them. The service adds the `wsl` host when it finds an administrator's relay, and those containers run while that user is signed in.
- **`native`** enables the Containers feature (and Hyper-V on Windows 10/11), installs `dockerd.exe`, `docker.exe` and the compose plugin in `C:\Program Files\Docker`, and registers the `docker` service. The `local` host then points at `npipe:////./pipe/docker_engine`. Windows Home has no Containers feature: use `wsl`.
- If Windows needs a restart to enable WSL or a feature, restart and run the same command again.

## Check how ducktainer reaches Docker

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

Run `sudo ducktainer setup` again whenever the Docker installation changes.
