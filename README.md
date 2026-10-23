<p align="center"><img src="assets/brand/ducktainer-logo.svg" alt="ducktainer: containerized applications with a squeak" width="420"></p>

# ducktainer

[![CI](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml/badge.svg)](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml)

Docker management in a single binary. ducktainer gives you a web UI for containers, compose stacks, images, networks and volumes, with live logs and terminals, an app marketplace, and private tunnels. You sign in with your system users, and only the addresses you allow can reach it.

[Documentation](https://leandrofus.github.io/ducktainer/docs/) · [Español](README.es.md) · [Русский](README.ru.md) · [Releases](https://github.com/leandrofus/ducktainer/releases)

## Features

- **Containers**: table or board views, live CPU, memory and network, logs, terminals, files and processes. Editing a container recreates it safely and restores the original if the new one fails.
- **Compose stacks**: a visual editor that keeps your YAML, comments and variables intact, multi-file projects, validation before saving, and combined logs.
- **Images, networks and volumes**: pull and push with per-layer progress, build from a Dockerfile, see what uses each network and volume, and clean up what nothing uses.
- **Marketplace**: built-in apps plus any Portainer template list, deployed as a stack you can edit.
- **Tunnels and publishing**: open a private tunnel to any container port, or publish a web port to the internet through Cloudflare.
- **Swarm**: manage the cluster, services, nodes, secrets, configs and swarm stacks.
- **Remote hosts**: reach other machines over SSH, over TLS, or through an outbound agent for hosts behind NAT. Nothing to install on the remote for SSH.
- **Access**: sign in with Linux or Windows accounts. Roles come from system groups, and the allowlist drops unknown connections before TLS.
- **Audit log**: every sign-in, change and terminal session is recorded.

The web UI and the CLI are available in English, Spanish and Russian.

## Install

### Linux packages

Packages are available for RHEL, Alma, Rocky and Fedora (rpm), and for Debian and Ubuntu (deb), on amd64 and arm64. Docker or a compatible engine must already be installed.

```sh
# RHEL / Fedora
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest.x86_64.rpm
sudo dnf install ./ducktainer-latest.x86_64.rpm

# Debian / Ubuntu
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer_latest_amd64.deb
sudo apt install ./ducktainer_latest_amd64.deb
```

The package starts the `ducktainer` service on `http://127.0.0.1:9900`. It also detects how Docker is installed, so the service can reach the socket.

### Windows

From an administrator PowerShell:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

The installer registers the `ducktainer` service on `http://127.0.0.1:9900`. Docker Desktop is not required: `ducktainer engine install` sets up the open-source Docker Engine.

### Single binary

Download the binary for your platform from the [releases page](https://github.com/leandrofus/ducktainer/releases), or build it yourself (see [Development](#development)).

```sh
./ducktainer serve     # http://127.0.0.1:9900, prints the access token
./ducktainer ui        # the same, and opens the browser
```

Without the Linux packages, sign-in uses the access token, since PAM is not available. Windows binaries also accept Windows accounts.

## First steps

1. **Check how ducktainer reaches Docker**: `sudo ducktainer setup --dry-run` shows the socket and the service user it would use. Run `sudo ducktainer setup` if the Docker installation changes.
2. **Give people access**: add their system user to `ducktainer-admins` (full access) or `ducktainer-viewers` (read-only).
3. **Open it to your network**: by default ducktainer only listens on the local machine. Allow the addresses that may connect, then restart:

   ```sh
   sudo ducktainer allow add 192.168.1.0/24
   sudo systemctl restart ducktainer
   ```

4. **Sign in** at `https://<server>:9900`. The self-signed certificate's fingerprint is printed at startup, so clients can pin it.

The [documentation](https://leandrofus.github.io/ducktainer/docs/) covers each step in detail, including remote hosts, Cloudflare publishing, the command line, and where ducktainer keeps its files.

## Security

- The service runs as an unprivileged user. Only password checks run as root, in a socket-activated helper.
- Secrets in the configuration are encrypted with AES-256-GCM, using a key kept outside the data directory. Back up `/etc/ducktainer/secret.key` separately.
- The emergency access token is stored only as a hash.

ducktainer controls the Docker API of every host it manages, which is equivalent to root on those hosts. Protect the ducktainer server and its admin accounts as you would the hosts themselves.

## Development

```sh
make help      # all targets
make run       # build and start ducktainer serve on http://127.0.0.1:9900
make test      # Go unit tests and the web typecheck
```

The code is organized as:

- `main.go`: the command-line entry point.
- `internal/`: the Go packages (server, Docker client, agent, auth, compose, and more).
- `web/`: the web UI (React and Vite).
- `site/`: the project website (Astro).
- `packaging/`: systemd units, PAM files and package definitions.

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to report a problem, set up the project, run the checks and send a change.

## License

ducktainer is free software under the **GNU Affero General Public License, version 3 or (at your option) any later version** (`AGPL-3.0-or-later`). The full text is in [LICENSE](LICENSE).
