<p align="center"><img src="assets/brand/ducktainer-logo.svg" alt="ducktainer: containerized applications with a squeak" width="420"></p>

# ducktainer

[![CI](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml/badge.svg)](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml)

**Administración de Docker en un único binario.** Una UI web tipo CRM (clic derecho en todo, vistas guardadas, kanban, paleta ⌘K), stacks compose con editor visual, marketplace de apps, logs en vivo y terminales web, acceso en un clic a cada puerto de cada contenedor, túneles privados y publicación por Cloudflare. Se entra con los **usuarios del sistema** (PAM), y el acceso por red se limita a los hosts que autorices.

[English](README.md) · [Русский](README.ru.md) · [sitio web](https://leandrofus.github.io/ducktainer/)

- **Nada que instalar en los hosts remotos**: se llega por **SSH** (`docker system dial-stdio`, como `DOCKER_HOST=ssh://`), por TLS (`tcp://`, por ejemplo un `ducktainer expose` remoto) o, para nodos detrás de NAT o cortafuegos, con un **agente saliente** opcional (el mismo binario) que se conecta al servidor.
- **Mínimo privilegio**: el servicio corre con un usuario sin privilegios. `ducktainer setup` lo adapta a cómo esté instalado Docker: grupo del socket, rootless o solo root. Solo la verificación de contraseñas corre como root, en un helper activado por socket.
- **Control de acceso**: lista de IPs/CIDR/nombres de host permitidos (el resto se corta antes del TLS), roles a partir de grupos POSIX, TLS automático con fijado de certificado y registro de auditoría.
- **Idiomas**: inglés, español y ruso. La UI sigue al navegador y se cambia desde el menú de usuario; la CLI usa `LANG` (por ejemplo `LANG=es_AR.UTF-8 ducktainer help`).

---

## Puesta en marcha

### 1. Instalar

**Paquetes (recomendado)**: RHEL/Alma/Rocky 9+, Fedora, Debian 12+ y Ubuntu 22.04+ (amd64 y arm64). El paquete depende de un motor compatible con Docker (`docker-ce`, `moby-engine`, `docker.io`, `podman-docker`…) y recomienda el plugin compose y el cliente SSH.

```sh
# RHEL / Fedora
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest.x86_64.rpm
sudo dnf install ./ducktainer-latest.x86_64.rpm

# Debian / Ubuntu
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer_latest_amd64.deb
sudo apt install ./ducktainer_latest_amd64.deb
```

En arm64 usa `ducktainer-latest.aarch64.rpm` o `ducktainer_latest_arm64.deb`. Estos enlaces apuntan siempre a la última versión.

El paquete:

- crea el usuario `ducktainer` y los grupos `ducktainer-admins` y `ducktainer-viewers`;
- crea `/etc/ducktainer/secret.key`, la clave que cifra los secretos de la configuración;
- ejecuta `ducktainer setup` para detectar cómo está instalado Docker (y, en máquinas con escritorio, prepara el menú de inicio);
- habilita `ducktainer.service` y el helper `ducktainer-pam.socket`, escuchando en `http://127.0.0.1:9900`.

Las actualizaciones conservan la configuración y solo reinician el servicio si estaba en marcha.

**Windows (MSI)**, desde un PowerShell de administrador:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

El instalador:

- pone `ducktainer.exe` en `C:\Program Files\ducktainer` y en el PATH;
- registra el servicio `ducktainer` (`ducktainer serve` como LocalSystem, que se reinicia si falla), escuchando en `http://127.0.0.1:9900`, y una entrada en el menú Inicio;
- guarda los datos en `C:\ProgramData\ducktainer`, que solo pueden leer SYSTEM y los Administradores. Desde una terminal de administrador, `ducktainer token`, `ducktainer allow` o `ducktainer auth` trabajan sobre la configuración del servicio.

Se entra con las cuentas de Windows (locales, o `DOMINIO\usuario`). Los miembros de Administradores, `docker-users` o `ducktainer-admins` son administradores; los de `ducktainer-viewers` tienen acceso de solo lectura. Los roles salen de los grupos locales, así que un grupo del dominio cuenta cuando es miembro de uno de ellos. Las actualizaciones conservan la configuración, y desinstalar deja `C:\ProgramData\ducktainer` en su sitio. Si la máquina todavía no tiene Docker, instálalo con `ducktainer engine install` (más abajo).

**Binario suelto** (cualquier Linux, macOS o Windows; sin PAM, en Linux y macOS solo se entra con token, y en Windows también con cuentas de Windows):

```sh
./ducktainer serve            # http://127.0.0.1:9900; muestra el token de acceso
./ducktainer ui               # lo mismo, y abre el navegador
```

Sin el paquete, los datos van al directorio de configuración del usuario (`~/.config/ducktainer` en Linux) o adonde apunte `DUCKTAINER_HOME`; `ducktainer help` muestra la ruta exacta.

**Windows sin Docker Desktop.** `ducktainer engine` instala el Docker Engine libre por ti, desde una terminal de administrador:

```powershell
.\ducktainer.exe engine install                 # contenedores Linux: Docker en una distribución WSL 2 propia
.\ducktainer.exe engine install --mode native   # contenedores Windows: dockerd.exe como servicio de Windows
.\ducktainer.exe engine status
.\ducktainer.exe ui
```

- `wsl` (por defecto) crea la distribución WSL 2 `ducktainer` con la imagen de Ubuntu (verificada con su SHA256SUMS), corre `docker.io` con systemd y añade el host `wsl` (`wsl://ducktainer`). Al iniciar sesión, una tarea arranca `ducktainer engine relay` para el usuario que hizo la instalación. El relay mantiene la distribución en marcha, para que los contenedores no se paren cuando WSL la apagaría. También sirve el Docker de la distribución por un named pipe, porque las distribuciones WSL son de un usuario y el servicio (LocalSystem) no puede arrancarlas. El servicio añade el host `wsl` cuando encuentra el relay de un administrador, y esos contenedores corren mientras ese usuario tenga la sesión iniciada (`--no-keepalive` omite la tarea).
- `native` activa la característica Containers (y Hyper-V en Windows 10/11), instala `dockerd.exe`, `docker.exe` y el plugin compose en `C:\Program Files\Docker`, registra el servicio `docker` y apunta el host `local` a `npipe:////./pipe/docker_engine`. `--version` y `--sha256` fijan la versión de Docker. Windows Home no tiene Containers: usa `wsl`.
- Si Windows pide reiniciar (para activar WSL o una característica), reinicia y vuelve a ejecutar el mismo comando.

### 2. Comprobar cómo llega a Docker

```sh
sudo ducktainer setup --dry-run
```

```
socket:  /var/run/docker.sock  (root:docker 0660, standard path)
mode:    group
  · access through group docker (socket root:docker 0660)
```

| Instalación detectada | El servicio corre como |
|---|---|
| Socket accesible por un grupo (`root:docker 0660`, con cualquier nombre de grupo) | `ducktainer` + ese grupo |
| Docker rootless (`/run/user/1000/docker.sock`) o Podman rootless (`/run/user/1000/podman/podman.sock`) | el dueño del socket |
| Socket solo de root, o Podman | root (con aviso) |
| Instalado pero sin arrancar | lo que indique la unit `docker.socket`; Podman sin socket de API activo: se habilita `podman.socket` solo (root) |

El resultado se escribe en `/etc/systemd/system/ducktainer.service.d/10-docker-access.conf`. Si cambias la instalación de Docker, vuelve a ejecutar `sudo ducktainer setup` (`--socket RUTA` fuerza un socket).

### 3. Dar acceso a personas

Las cuentas son usuarios normales del sistema (locales o de LDAP/SSSD/FreeIPA). El **rol** sale de sus grupos:

| Rol | Grupos (por defecto) | Puede |
|---|---|---|
| **Administrador** | `ducktainer-admins`, `docker` | todo: contenedores, stacks, imágenes, terminales, túneles, configuración y usuarios |
| **Solo lectura** | `ducktainer-viewers` | ver contenedores, stacks, imágenes, redes, volúmenes y hosts; no puede modificar nada, abrir terminales, explorar ni descargar archivos, ni ver el resto de la configuración. Las contraseñas de las cadenas de conexión se ocultan |

```sh
sudo useradd -m ana && sudo passwd ana       # solo si la cuenta no existe
sudo usermod -aG ducktainer-admins ana          # administradora (o ducktainer-viewers: solo lectura)
sudo ducktainer auth test ana                   # comprueba la contraseña y muestra el rol
```

- La pertenencia a los grupos se revisa cada 5 minutos: si quitas a alguien de un grupo, pierde el acceso enseguida.
- Los grupos se cambian, y se pueden añadir usuarios sueltos, con `ducktainer auth set --admin-groups … --viewer-groups … --admin-users … --viewer-users …`. Las sesiones duran 12 horas (`--session-hours`).
- `root` no puede entrar salvo que lo habilites (`ducktainer auth set --allow-root on`).
- 5 intentos fallidos en 10 minutos bloquean nuevos intentos hasta que el más antiguo cumpla 10 minutos.
- La UI lo muestra en **Configuración → Acceso → Personas**: los grupos de cada rol, sus miembros actuales y una guía que te escribe estos comandos.

### 4. Abrirlo a la red

Por defecto ducktainer solo escucha en la propia máquina. Autoriza las direcciones que pueden conectarse (IPs, rangos CIDR o nombres de host; los nombres se vuelven a resolver cada minuto, así que el DNS dinámico funciona) y reinicia:

```sh
sudo ducktainer allow add 192.168.1.0/24 oficina.ejemplo.com
sudo systemctl restart ducktainer       # ahora escucha en 0.0.0.0:9900, con TLS
```

- Cualquier otro origen se desconecta antes del TLS. La propia máquina siempre está permitida.
- En **Configuración → Acceso → Red** ves la dirección de escucha, tu IP y la regla que te deja pasar. Puedes editar la lista (se aplica al instante) y comprobar si una dirección estaría permitida.
- El certificado TLS es autofirmado. Su huella SHA-256 se imprime al arrancar para que los clientes la fijen. Para usar tu propio certificado, añade `--cert/--key` a `DUCKTAINER_OPTS` (`/etc/ducktainer/ducktainer.env`); `--tls on|off` cambia la elección automática.
- Los túneles privados (ver [Funciones](#funciones)) usan los puertos `41000-41099` del servidor (`--tunnel-ports`): ábrelos en el cortafuegos si quieres usarlos.

### 5. Entrar

Abre `https://<servidor>:9900` y entra con tu usuario y contraseña del sistema.

El **token de emergencia** entra como administrador y autoriza la API (`Authorization: Bearer`). Solo se guarda su hash, así que se muestra una vez: `sudo ducktainer token --rotate` crea e imprime uno nuevo. Para permitir solo usuarios del sistema, desactívalo con `sudo ducktainer auth set --token-login off`. **Configuración → Acceso → Inicio de sesión** tiene los mismos interruptores.

### 6. Más hosts Docker (opcional)

```sh
# con el agente saliente (ideal para nodos detrás de NAT/cortafuegos): da de alta
# el host (Configuración → Hosts → Agente (conexión saliente / Edge), o la línea de abajo), que recibe un token
# propio, y ejecuta en el nodo el comando de "Copiar comando de instalación del agente":
ducktainer endpoint add worker-1 agent://          # imprime el token del host
#   curl -fsSL -k --pinnedpubkey 'sha256//…' -H 'Authorization: Bearer <token del host>' \
#     'https://<servidor-ducktainer>:9900/api/agent/install.sh?server=…' | sudo sh

# por SSH: nada que instalar en el remoto (acceso por clave; el usuario debe poder usar Docker)
sudo -u ducktainer ssh-keygen -t ed25519          # clave en /var/lib/ducktainer/.ssh; añádela al authorized_keys del remoto
ducktainer endpoint add prod ssh://deploy@prod.ejemplo.com      # --key ARCHIVO para usar otra clave

# o con el proxy, ejecutado en el remoto:
ducktainer expose --allow <ip-del-servidor-ducktainer>   # imprime la línea `ducktainer endpoint add …` exacta para ejecutar aquí
```

Los hosts también se gestionan desde **Configuración → Hosts**, y se cambia entre ellos desde la parte superior de la barra lateral.

- Cada host con agente tiene su propio token, válido solo para ese host; el token de administrador no se acepta desde agentes. El comando lo envía en una cabecera, fija el certificado de este servidor (sin `--insecure`), instala el agente como servicio systemd y deja el token en el nodo en `/etc/ducktainer-agent/agent.env` (solo root). Los hosts registrados por versiones anteriores con el token de administrador se rechazan hasta que les des un token en Configuración → Hosts y reinstales el agente.
- Si los agentes no llegan al puerto protegido por la lista, `serve --agent-listen :9901` abre un puerto solo para agentes (con token, sin lista de IPs).
- Los agentes se mantienen en la versión del servidor: cuando cambia, descargan el binario nuevo, comprueban que corre en el nodo y se reinician con él, conservando el actual si algo falla (se desactiva por host en Ajustes → Hosts; las compilaciones `dev` y Windows nunca se actualizan solas). Los paquetes incluyen binarios estáticos del agente para amd64 y arm64, así que los nodos no necesitan una glibc concreta (RHEL 8 funciona) y pueden ser de otra arquitectura que el servidor. Los agentes instalados antes de esta versión hay que reinstalarlos una vez.
- Los **nodos Windows** también funcionan como agentes: en Ajustes → Hosts elige *Windows* y pega el comando de PowerShell en una consola elevada del nodo. Comprueba antes el nodo (arquitectura, que alcanza el servidor con el certificado fijado, que el binario corre), instala el servicio de Windows `ducktainer-agent` (el token queda en un archivo que solo leen Administradores y SYSTEM) y llega al motor Docker por su named pipe. Todos los servidores —los paquetes de Linux y el MSI— llevan los agentes de ambos sistemas (Linux amd64/arm64, Windows amd64) y entregan a cada nodo el suyo. En Windows los agentes también se actualizan solos.
- Los hosts `tcp://` deben usar TLS salvo que estén en la misma máquina; sin `--fingerprint`, se fija el certificado que presenten al añadirlos.
- Los hosts SSH registran la clave del host en la primera conexión (`StrictHostKeyChecking=accept-new`); una clave cambiada se rechaza.
- Las credenciales de registries pueden limitarse a algunos hosts (`--hosts` o en la UI): compose ejecutado en un nodo recibe copia de las credenciales que ese host puede ver.

#### Modelo de confianza

ducktainer controla la API de Docker de cada host, lo que equivale a root en ellos: quien controle el servidor ducktainer (o sus cuentas de administrador) controla todos los hosts. Protege esa máquina y `/etc/ducktainer/secret.key` como a los propios hosts. Para editar stacks en un nodo, ducktainer arranca allí un contenedor auxiliar (`ducktainer-files`) que monta `/` del nodo; se cierra tras una hora sin uso.

---

## Funciones

- **Accesos** (la página inicial de cada host): todos los puertos de todos los contenedores, separados en puertos web (se abren con un clic) y puertos TCP/UDP. Para servicios conocidos (PostgreSQL, MySQL/MariaDB, MongoDB, Redis, RabbitMQ) arma la cadena de conexión con las credenciales que encuentra en el entorno de compose. Las etiquetas de compose mandan sobre la detección: `ducktainer.url=https://app.ejemplo.com`, `ducktainer.url.<puerto>=…` y `ducktainer.kind.<puerto>=http|tcp`.
- **Túneles privados**: clic derecho en un puerto → "Abrir un túnel…" para llegar a él a través del servidor ducktainer, también a puertos no publicados y a hosts detrás de un agente o NAT. Un túnel se cierra tras 30 minutos sin conexiones, a una hora fija, o queda abierto (también tras reinicios) hasta que lo cierres. Acepta solo la dirección que lo abrió, o todas las que pueden llegar a ducktainer.
- **Publicación en internet con Cloudflare**: "Exponer a internet…" en un puerto web lo publica con `cloudflared` (se descarga y verifica su checksum si no está instalado). Sin cuenta obtienes una dirección `trycloudflare.com` aleatoria; con un token de API (Configuración → Cloudflare) cada servicio recibe un nombre fijo en tus propios dominios. El acceso puede ser público o limitarse a algunas IPs/hosts, a quien entre en ducktainer (opcionalmente solo algunos usuarios o grupos) o a quien tenga un enlace secreto, y puede caducar.
- **Menú de inicio**: en una máquina con escritorio, las apps web de sus contenedores se pueden añadir a un menú "Containers" (Linux, entradas freedesktop) o a Inicio › Programas › Containers (Windows), una a una o automáticamente; el menú se actualiza solo (en Linux, mediante una unidad de usuario de systemd que `ducktainer setup` instala para todos los usuarios). **Configuración → Acceso → Menú inicio**.
- **Contenedores**: tabla agrupada por stack o kanban por estado (arrastra una tarjeta a "Detenido" para pararlo), vistas guardadas, acciones en lote y vista rápida. Cada ficha tiene Resumen (CPU/memoria/red en vivo y actividad), Logs, Terminal, Archivos, Rendimiento, Procesos, Configuración e Inspección.
- **Editar contenedores**: Docker no permite modificar un contenedor en el sitio, así que ducktainer crea uno nuevo con los cambios, intercambia los nombres y borra el viejo. Los volúmenes se conservan y, si el nuevo no arranca, se restaura el original. También se pueden renombrar, duplicar, actualizar (pull + recrear) y guardar como imagen (commit).
- **Stacks compose**:
  - **editor visual** (el YAML sigue mandando: se conservan comentarios, formato y variables) y editor YAML;
  - proyectos de varios archivos (base + overrides) con vista combinada y "fusionar en un solo archivo";
  - validación antes de guardar (`docker compose config`), despliegue y actualización, logs combinados por servicio;
  - los stacks creados fuera de ducktainer se detectan y se editan en su sitio, o se adoptan;
  - un stack puede vivir en el almacén de ducktainer o en el directorio que elijas, también en nodos remotos.
- **Modo Swarm**: si un host forma parte de un swarm, aparece un grupo Swarm en la barra lateral:
  - **Clúster**: inicializar un swarm o unirse a uno, comandos de unión para workers y managers (con renovación de tokens), salir del swarm;
  - **Servicios**: crear (imagen, réplicas o global, puertos, entorno, redes overlay, secrets, configs, restricciones, límites), escalar, reiniciar, actualizar la imagen, revertir, editar la especificación completa, tareas por nodo y logs en vivo;
  - **Stacks swarm** desplegados con `docker stack deploy` a partir de un compose y su `.env` (los mismos editores que los stacks compose). Como los stacks compose, viven en el almacén de ducktainer o en un directorio propio, también en nodos remotos; los servicios con `build:` (y un `image:` que nombre el resultado) se construyen ahí antes de desplegar y, opcionalmente, se suben a su registro para que todos los nodos puedan descargarlos. Los desplegados por fuera se listan y se pueden adoptar pegando su compose o indicando su directorio. Las plantillas Swarm del marketplace se despliegan aquí;
  - **Nodos**: disponibilidad (activo, pausa, drenar), promover/degradar, labels, tareas por nodo, quitar;
  - **Secrets y configs**: crear desde texto o un archivo, ver qué servicios los usan.
  En un worker solo se ve la página del clúster: el swarm se gestiona desde un host manager.
- **Imágenes**: pull y push con progreso por capa, etiquetar, construir desde un Dockerfile, historial de capas, limpieza.
- **Redes**: registro con contenedores conectados y sus IPs, mapa de conexiones, crear, conectar y desconectar.
- **Volúmenes**: quién usa cada uno, huérfanos, tamaños y explorar su contenido a través de un contenedor en marcha que los monte.
- **Marketplace**: 26 apps incluidas más cualquier lista de plantillas Portainer v2/v3. La lista oficial de Portainer viene activada; la comunitaria (~800 apps, Lissy93) viene configurada pero desactivada. Las fuentes se gestionan en Configuración → Marketplace.
- **Panel y actividad**: resumen del host, uso de disco y limpieza, y el flujo de eventos de Docker en vivo.
- **Auditoría**: cada inicio de sesión y cada acción de escritura o de terminal, en Configuración → Auditoría y en `journalctl -u ducktainer`.

## Línea de comandos

| Comando | Para qué |
|---|---|
| `ducktainer serve` | UI web + API (`--listen`, `--allow`, `--tls auto\|on\|off`, `--cert/--key`, `--expose :2376 [--expose-read-only] [--expose-no-exec]`, `--agent-listen :9901`, `--tunnel-ports 41000-41099`) |
| `ducktainer ui` | Como `serve` en 127.0.0.1, y abre el navegador |
| `ducktainer agent` | Agente saliente (`--server`, `--name`, `--fingerprint`, `--socket`, `--install-service`; token en `DUCKTAINER_AGENT_TOKEN`) |
| `ducktainer expose` | Publica solo la API de Docker, protegida (lista + token + TLS; `--read-only`, `--no-exec`) |
| `ducktainer engine install [--mode wsl\|native] \| status \| relay` | Windows: instala Docker sin Docker Desktop |
| `ducktainer setup [--dry-run] [--socket RUTA]` | Adapta el servicio a la instalación de Docker |
| `ducktainer install-service [opciones de serve]` | Instala un servicio systemd para el binario suelto (root) |
| `ducktainer auth show \| set … \| test <usuario>` | Inicio de sesión con usuarios del sistema y roles |
| `ducktainer allow ls \| add \| rm` | Lista de direcciones permitidas |
| `ducktainer token [--rotate]` | Crea e imprime un token de emergencia/API nuevo (solo se guarda su hash, así que sin `--rotate` solo se muestra justo después de crearse) |
| `ducktainer endpoint ls \| add \| rm` | Hosts Docker (`unix://`, `ssh://usuario@host`, `tcp://host:2376`, `agent://`, `wsl://distro`) |
| `ducktainer registry ls \| add \| rm` | Credenciales de registries privados (contraseña por stdin; `--hosts` limita qué hosts las reciben) |
| `ducktainer ps \| logs \| sh` | Contenedores por stack · logs de un contenedor o de un stack entero · shell interactiva (elige bash/ash/sh); `-e <host>` elige el host |
| `ducktainer docker-env <host>` | `eval "$(ducktainer docker-env prod)"` para usar la CLI de docker contra un host |
| `ducktainer version` | Muestra la versión |

## Archivos

| Ruta | Contenido |
|---|---|
| `/etc/ducktainer/ducktainer.env` | `DUCKTAINER_OPTS` para `ducktainer serve` |
| `/etc/ducktainer/secret.key` | clave que cifra los secretos de la configuración (solo root; llega al servicio con `LoadCredential` de systemd) |
| `/etc/pam.d/ducktainer` | pila PAM del inicio de sesión (`password-auth` en RHEL, `common-auth` en Debian) |
| `/var/lib/ducktainer/` | `config.json`, stacks (`stacks/<host>/<nombre>`), auditoría, certificado TLS, claves SSH del servicio, el `cloudflared` descargado |
| `/etc/systemd/system/ducktainer.service.d/10-docker-access.conf` | lo escribe `ducktainer setup` |
| `/usr/local/share/applications/ducktainer-containers/` | entradas del menú de inicio (máquinas con escritorio) |

Los secretos de `config.json` (secreto de sesión, tokens de hosts, contraseñas de registries, tokens de Cloudflare, enlaces secretos) se cifran con AES-256-GCM, y del token de emergencia solo se guarda su hash SHA-256. La clave está separada de los datos, así que una copia o backup de `/var/lib/ducktainer` no revela nada sin ella. Haz backup de `/etc/ducktainer/secret.key` por separado: sin ella la configuración no se puede leer. Con el binario sin paquete, la clave es `secret.key` en el directorio de datos, salvo que `DUCKTAINER_KEY_FILE` apunte a otro sitio. Las configuraciones en texto plano de versiones anteriores se cifran al primer arranque.

## Idiomas

El inglés es el idioma de origen. Para añadir uno:

1. Copia `web/src/i18n/es.ts` (UI) e `internal/i18n/es.go` (errores de la API y CLI). Las claves son los textos en inglés; las claves `ctx|texto` son variantes de género/número que usa `tc()`.
2. Registra el nuevo catálogo en `web/src/i18n/index.ts` y en `internal/i18n/i18n.go`.

## Desarrollo

```sh
make help           # todos los comandos disponibles
make run            # compila y ejecuta ducktainer serve en local (http://127.0.0.1:9900; ARGS="..." para opciones)
make ui             # compila y ejecuta ducktainer ui (abre el navegador)
make build          # UI web + binario estático (sin PAM) en bin/
make build-pam      # con PAM (necesita gcc y pam-devel / libpam0g-dev)
make test           # tests de Go + chequeo de tipos de la web (test-unit, test-race, test-web por separado)
make dev            # UI con hot reload contra un `ducktainer serve` en :9900
make packages       # rpm + deb (amd64, arm64) construidos en un contenedor; solo hace falta Docker
make dist           # binarios estáticos para linux/darwin/windows
make clean          # borra los artefactos de compilación
```

Estructura:

- `main.go`: CLI.
- `internal/server`: API HTTP/WebSocket, pantalla Accesos, túneles, exposiciones por Cloudflare, sincronización del menú de inicio.
- `internal/docker`: cliente de la Engine API (unix, ssh, tcp+tls, npipe, agente).
- `internal/agent`: agente saliente y su túnel.
- `internal/auth`: PAM, roles y el helper de root.
- `internal/setup`: detección de la instalación de Docker.
- `internal/engine`: instalación de Docker en Windows (WSL 2 / nativo).
- `internal/cloudflare`: `cloudflared` y la API de Cloudflare.
- `internal/shortcuts`: entradas del menú de inicio (Linux, Windows).
- `internal/compose`, `internal/templates`, `internal/proxy`, `internal/access`, `internal/config`, `internal/probe`, `internal/tlsutil`, `internal/cli`, `internal/i18n`.
- `packaging/`: units de systemd, archivos PAM, nfpm.
- `web/`: React + Vite.
- `site/`: el sitio web (Astro).

## Licencia

ducktainer es software libre bajo la **GNU Affero General Public License, versión 3 o (a tu elección) cualquier versión posterior** (`AGPL-3.0-or-later`). El texto completo está en [`LICENSE`](LICENSE).
