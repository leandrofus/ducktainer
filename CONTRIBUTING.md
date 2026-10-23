# Contributing to ducktainer

Thanks for helping. This guide covers how to report a problem, set up the project, run the checks, and send a change. Everyone who takes part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

To talk with other users and contributors, join the [Discord server](https://discord.gg/H28S6chSy). Use it for questions and ideas; bugs and feature requests still go in GitHub issues, so they can be tracked.

## Reporting a problem

Open an issue at <https://github.com/leandrofus/ducktainer/issues>. Include:

- the ducktainer version (`ducktainer --version`) and how you installed it (rpm, deb, MSI, or binary);
- your operating system and the Docker version;
- the steps to reproduce, and what you expected instead;
- the relevant lines from the audit log or the system journal, if any.

For a security vulnerability, do not open a public issue. Report it privately as described in [SECURITY.md](SECURITY.md).

## Setting up

You need:

- **Go**, the version in [`go.mod`](go.mod).
- **Node.js 22** and **npm** for the web UI and the site. [Bun](https://bun.sh) also works; the Makefile uses it when it is installed.
- **Docker**, to run the service against a real engine.
- **gcc and the PAM headers** (`libpam0g-dev` on Debian/Ubuntu, `pam-devel` on Fedora/RHEL) only if you build with PAM (`make build-pam`).

Clone the repository and run:

```sh
make help      # all targets
make run       # build and start ducktainer serve on http://127.0.0.1:9900
make dev       # UI with hot reload against a running service
```

The layout of the code is described in the [Development section of the README](README.md#development).

## Checks

Before you open a pull request, run:

```sh
make test                    # Go unit tests and the web typecheck
go vet ./...                 # what CI runs besides the tests
go test -race ./...          # the race detector, as in CI
```

CI runs these on every pull request, and also builds the static and PAM binaries, checks the packaging scripts with `sh -n`, and builds the site. A pull request needs all of them green.

For the site:

```sh
cd site
npm ci
npm run dev      # http://localhost:4321/ducktainer/
npm run build    # the same build the Pages workflow runs
```

## Making a change

1. Open an issue first for anything large or that changes behavior, so we can agree on the approach.
2. Branch from `main`.
3. Keep the change focused. One pull request should do one thing.
4. Add or update tests when you change Go code that has them (`*_test.go`).
5. Update the documentation when you change a command, a flag, a setting, or a screen. The English docs live in `site/src/content/docs/en/`, and the README files describe the same behavior in Spanish and Russian.

### Commit messages

Start the subject with the area you changed, followed by a colon, then what changed in plain words:

```
site: one language menu instead of a pill per language
Windows service: create the config once at startup, retry the rename
```

Keep the subject short. Use the body to explain why when the reason is not obvious from the diff.

### Pull requests

Describe what the change does and how you checked it. Link the issue it closes. Draft pull requests are welcome for work in progress.

## Translations

English is the source language. To add or update a language:

1. **Web UI:** copy `web/src/i18n/es.ts` to a new file for your language, and register it in `web/src/i18n/index.ts`. Keys are the English strings; `ctx|text` keys are gender and number variants used by `tc()`.
2. **API errors and CLI:** copy `internal/i18n/es.go` and register it in `internal/i18n/i18n.go`.
3. **Website:** add the page copy to `site/src/i18n.ts`, the documentation pages to `site/src/content/docs/<lang>/`, and the language to the `locales` list in `site/astro.config.mjs`.

Run `make test` and `npm run build` in `site/` afterwards.

## Licensing

ducktainer is licensed under the GNU Affero General Public License, version 3 or later (see [`LICENSE`](LICENSE)). By submitting a contribution, you agree that it is released under the same license.
