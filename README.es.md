<p align="center"><img src="assets/brand/ducktainer-logo.svg" alt="ducktainer: aplicaciones en contenedores, con un cuac" width="420"></p>

# ducktainer

[![CI](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml/badge.svg)](https://github.com/leandrofus/ducktainer/actions/workflows/ci.yml)

Administración de Docker en un solo binario. ducktainer te da una interfaz web para contenedores, stacks compose, imágenes, redes y volúmenes, con logs y terminales en vivo, un marketplace de aplicaciones y túneles privados. Entras con tus usuarios del sistema, y solo llegan a él las direcciones que permitas.

[Documentación](https://leandrofus.github.io/ducktainer/es/docs/) · [English](README.md) · [Русский](README.ru.md) · [Versiones](https://github.com/leandrofus/ducktainer/releases)

## Características

- **Contenedores**: vistas de tabla o de tablero, CPU, memoria y red en vivo, logs, terminales, archivos y procesos. Editar un contenedor lo recrea de forma segura y restaura el original si el nuevo no arranca.
- **Stacks compose**: un editor visual que mantiene intactos tu YAML, comentarios y variables; proyectos de varios archivos, validación antes de guardar y logs combinados.
- **Imágenes, redes y volúmenes**: descarga y sube con progreso por capa, construye desde un Dockerfile, mira qué usa cada red y volumen, y limpia lo que nadie usa.
- **Marketplace**: aplicaciones incluidas y cualquier lista de plantillas de Portainer, desplegadas como un stack editable.
- **Swarm**: administración del clúster, servicios, nodos, secretos, configs y stacks de swarm.
- **Hosts remotos**: se llega a otras máquinas por SSH, por TLS o con un agente saliente para hosts detrás de NAT. Con SSH no hay que instalar nada en el remoto.
- **Acceso**: entras con cuentas de Linux o de Windows. Los roles salen de los grupos del sistema, y la lista de direcciones descarta las conexiones no permitidas antes del TLS.
- **Auditoría**: cada inicio de sesión, cambio y sesión de terminal queda registrado.

La interfaz web y la CLI están disponibles en inglés, español y ruso.

## Instalación

### Paquetes de Linux

Hay paquetes para RHEL, Alma, Rocky y Fedora (rpm), y para Debian y Ubuntu (deb), en amd64 y arm64. Docker o un motor compatible tiene que estar instalado.

```sh
# RHEL / Fedora
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest.x86_64.rpm
sudo dnf install ./ducktainer-latest.x86_64.rpm

# Debian / Ubuntu
curl -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer_latest_amd64.deb
sudo apt install ./ducktainer_latest_amd64.deb
```

El paquete arranca el servicio `ducktainer` en `http://127.0.0.1:9900`. También detecta cómo está instalado Docker, para que el servicio pueda llegar al socket.

### Windows

Desde un PowerShell de administrador:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

El instalador registra el servicio `ducktainer` en `http://127.0.0.1:9900`. No hace falta Docker Desktop: `ducktainer engine install` instala el motor Docker de código abierto.

### Binario único

Descarga el binario de tu plataforma desde la [página de versiones](https://github.com/leandrofus/ducktainer/releases), o compílalo tú mismo (ver [Desarrollo](#desarrollo)).

```sh
./ducktainer serve     # http://127.0.0.1:9900, imprime el token de acceso
./ducktainer ui        # lo mismo, y abre el navegador
```

Sin los paquetes de Linux, el inicio de sesión usa el token de acceso, porque el binario estático no incluye PAM. Los binarios de Windows también aceptan cuentas de Windows.

## Primeros pasos

1. **Revisa cómo llega ducktainer a Docker**: `sudo ducktainer setup --dry-run` muestra el socket y el usuario del servicio que usaría. Ejecuta `sudo ducktainer setup` si cambia la instalación de Docker.
2. **Da acceso a las personas**: agrega su usuario del sistema a `ducktainer-admins` (acceso completo) o a `ducktainer-viewers` (solo lectura).
3. **Abre el acceso a tu red**: por defecto, ducktainer solo escucha en la máquina local. Permite las direcciones que pueden conectarse y reinicia:

   ```sh
   sudo ducktainer allow add 192.168.1.0/24
   sudo systemctl restart ducktainer
   ```

4. **Entra** en `https://<servidor>:9900`. La huella del certificado autofirmado se imprime al arrancar, para que los clientes puedan fijarla.

La [documentación](https://leandrofus.github.io/ducktainer/es/docs/) explica cada paso con detalle, incluidos los hosts remotos, la publicación con Cloudflare, la línea de comandos y dónde guarda ducktainer sus archivos.

## Seguridad

- El servicio corre como usuario sin privilegios. Solo las comprobaciones de contraseña se ejecutan como root, en un ayudante activado por socket.
- Los secretos de la configuración se cifran con AES-256-GCM, usando una clave guardada fuera del directorio de datos. Haz copia de `/etc/ducktainer/secret.key` por separado.
- El token de acceso de emergencia se guarda solo como hash.

ducktainer controla la API de Docker de cada host que administra, lo que equivale a root en esos hosts. Protege el servidor de ducktainer y sus cuentas de administrador igual que los propios hosts.

## Desarrollo

```sh
make help      # todos los objetivos
make run       # compila y arranca ducktainer serve en http://127.0.0.1:9900
make test      # pruebas de Go y comprobación de tipos de la web
```

El código está organizado así:

- `main.go`: el punto de entrada de la línea de comandos.
- `internal/`: los paquetes de Go (servidor, cliente de Docker, agente, autenticación, compose, y más).
- `web/`: la interfaz web (React y Vite).
- `site/`: el sitio del proyecto (Astro).
- `packaging/`: unidades de systemd, archivos de PAM y definiciones de los paquetes.

Consulta [CONTRIBUTING.md](CONTRIBUTING.md) para saber cómo reportar un problema, preparar el proyecto, correr las comprobaciones y enviar un cambio.

## Licencia

ducktainer es software libre bajo la **GNU Affero General Public License, versión 3 o (a tu elección) cualquier versión posterior** (`AGPL-3.0-or-later`). El texto completo está en [LICENSE](LICENSE).
