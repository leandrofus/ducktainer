---
title: Instalación
description: Paquetes para RHEL, Fedora, Debian y Ubuntu, el binario suelto, Docker en Windows y cómo llega ducktainer a Docker.
order: 2
---

## Paquetes

Los paquetes son la forma recomendada. Sirven para RHEL/Alma/Rocky 9+, Fedora, Debian 12+ y Ubuntu 22.04+, en amd64 y arm64. Docker tiene que estar instalado, porque el paquete depende de él.

Descarga el paquete de tu sistema e instálalo (todos los archivos están en la [página de versiones](https://github.com/leandrofus/ducktainer/releases/latest)):

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
- detecta cómo está instalado Docker (`ducktainer setup`);
- deja el servicio `ducktainer` escuchando en `http://127.0.0.1:9900`.

## Windows (MSI)

Desde un PowerShell de administrador:

```powershell
curl.exe -fLO https://github.com/leandrofus/ducktainer/releases/latest/download/ducktainer-latest-x64.msi
Start-Process msiexec -Wait -ArgumentList '/i ducktainer-latest-x64.msi'
```

El instalador:

- pone `ducktainer.exe` en `C:\Program Files\ducktainer` y en el PATH;
- registra el servicio `ducktainer` (`ducktainer serve` como LocalSystem, que se reinicia si falla), escuchando en `http://127.0.0.1:9900`, y una entrada en el menú Inicio;
- guarda los datos en `C:\ProgramData\ducktainer`, que solo pueden leer SYSTEM y los Administradores. Desde una terminal de administrador, `ducktainer token`, `ducktainer allow` o `ducktainer auth` trabajan sobre la configuración del servicio.

Se entra con las cuentas de Windows (locales, o `DOMINIO\usuario`). Los miembros de Administradores, `docker-users` o `ducktainer-admins` son administradores; los de `ducktainer-viewers` tienen acceso de solo lectura. Los roles salen de los grupos locales, así que un grupo del dominio cuenta cuando es miembro de uno de ellos. Las actualizaciones conservan la configuración, y desinstalar deja `C:\ProgramData\ducktainer` en su sitio. Si la máquina todavía no tiene Docker, instálalo con `ducktainer engine install` (más abajo).

## Binario suelto

El binario funciona en Linux, macOS y Windows. Sin el paquete no hay PAM, así que en Linux y macOS se entra con el token que imprime al arrancar. En Windows el binario también admite cuentas de Windows.

```sh
./ducktainer serve            # http://127.0.0.1:9900; muestra el token
./ducktainer ui               # lo mismo, y abre el navegador
```

## Windows sin Docker Desktop

`ducktainer engine` instala por ti Docker Engine, de código abierto. Ejecútalo desde una terminal de administrador:

```powershell
.\ducktainer.exe engine install                 # contenedores Linux: Docker en una distribución WSL 2 propia
.\ducktainer.exe engine install --mode native   # contenedores Windows: dockerd.exe como servicio de Windows
.\ducktainer.exe engine status
.\ducktainer.exe ui
```

- **`wsl`** (por defecto) crea la distribución WSL 2 `ducktainer` a partir de la imagen de Ubuntu, verificada con los SHA256SUMS de Ubuntu. Ahí corre `docker.io` con systemd y añade el host `wsl` (`wsl://ducktainer`). Al iniciar sesión, una tarea arranca `ducktainer engine relay` para el usuario que hizo la instalación. El relay mantiene la distribución en marcha, para que los contenedores no se paren cuando WSL la apagaría. También sirve el Docker de la distribución por un named pipe, porque las distribuciones WSL son de un usuario y el servicio (LocalSystem) no puede arrancarlas. El servicio añade el host `wsl` cuando encuentra el relay de un administrador, y esos contenedores corren mientras ese usuario tenga la sesión iniciada.
- **`native`** activa la característica Containers (y Hyper-V en Windows 10/11), instala `dockerd.exe`, `docker.exe` y el plugin compose en `C:\Program Files\Docker` y registra el servicio `docker`. El host `local` pasa a apuntar a `npipe:////./pipe/docker_engine`. Windows Home no tiene Containers: usa `wsl`.
- Si Windows necesita reiniciar para activar WSL o una característica, reinicia y vuelve a ejecutar el mismo comando.

## Comprobar cómo llega a Docker

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

Si cambias la instalación de Docker, vuelve a ejecutar `sudo ducktainer setup`.
