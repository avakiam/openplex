<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img src="docs/assets/logo-light.svg" alt="OpenPlex" height="56">
  </picture>
</h1>

[English](README.md) · **Español** · [Català](README.ca.md)

OpenPlex es un panel de control de hosting web de código abierto, una
alternativa a Plesk y cPanel basada en contenedores. Se instala con un solo
comando en un servidor Ubuntu o Debian limpio y te ofrece, a ti, a tu equipo y
a tus clientes, un panel moderno para webs, bases de datos, correo, DNS,
copias de seguridad y seguridad.

> **Estado: versión previa.** OpenPlex está en pleno desarrollo y aún no ha
> tenido su primera versión pública. Puede haber cambios incompatibles hasta
> la 1.0.

## Por qué OpenPlex

- **Un solo binario.** `openplexd` es un único programa en Go con el panel
  incluido y su estado en una base de datos SQLite integrada. En el servidor
  solo hace falta Docker.
- **Cada web en su propio contenedor**, con su propio usuario, en una red
  privada por cuenta. La web de un cliente no puede leer, alcanzar ni frenar
  la de otro.
- **HTTPS automático** para cada web, alias y el propio panel, mediante un
  proxy Caddy (Let's Encrypt / ZeroSSL).
- **Software libre** (AGPL-3.0) con un modelo de extensiones abierto:
  cualquiera puede crear extensiones, gratuitas o de pago.

## Funciones

| | |
|---|---|
| **Webs** | Estáticas, PHP 7.4–8.4 (con las extensiones que necesitan WordPress, Laravel o Drupal), Node.js, Python y Go; configuración de PHP por web (memoria, tamaño de subida, tiempos, zona horaria…); WordPress, Joomla y Drupal con un clic (última versión, base de datos propia, en inglés, español o catalán) y sus actualizaciones, con copia de seguridad previa; copias de staging en un subdominio que se envían a producción; alias y subdominios; redirecciones; variables de entorno; arrancar/parar/reiniciar; registros de acceso y de errores; límites de recursos |
| **Archivos** | Gestor de archivos web con editor, subida, comprimir/descomprimir y permisos; SFTP (puerto 2222, claves SSH o una contraseña solo para SFTP); terminal web en el contenedor de la web |
| **Bases de datos** | MariaDB, PostgreSQL y Redis; Adminer con un clic; rotación de contraseñas |
| **Correo** | Buzones, alias y reenvíos, cuotas, registros DKIM/SPF/DMARC, webmail Roundcube con acceso en un clic |
| **DNS** | DNS autoritativo (PowerDNS) con zonas creadas para tus dominios |
| **Copias de seguridad** | Copias cifradas y deduplicadas (restic) en un disco local o en S3, programadas; restauración de una web, una base de datos, un dominio de correo o un solo buzón; reconstrucción completa del servidor con `openplexd restore-panel` |
| **Seguridad** | Cortafuegos del servidor (nftables) con aplicación segura y vuelta atrás, Fail2Ban, captcha en el inicio de sesión (Turnstile, hCaptcha, reCAPTCHA), verificación en dos pasos, registro de auditoría |
| **Usuarios** | Administradores, revendedores y clientes con cuotas; compartir una web (solo algunas áreas, opcionalmente en solo lectura) o una base de datos con otro usuario; claves de API |
| **Tareas y monitorización** | Tareas programadas por web; CPU, memoria, disco y red del servidor y de los contenedores; alertas por correo (copia fallida, web caída, disco casi lleno, certificado sin renovar, actualizaciones) |
| **Idiomas** | Inglés, español y catalán |

## Requisitos

- Ubuntu 22.04 o superior, o Debian 12 o superior, en amd64 o arm64.
- Acceso root, una IP pública y los puertos 80 y 443 libres.
- Se recomiendan 2 GB de RAM o más (el correo y las bases de datos son lo que más usa).
- Un dominio que apunte al servidor, para el panel y para HTTPS.

## Instalación

Cuando se publique la primera versión:

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | sudo bash -s -- \
  --panel-domain panel.example.com --acme-email tu@example.com
```

El instalador configura Docker, el servicio `openplexd` y el cortafuegos, y
muestra la dirección del panel. Entra como `admin` con la contraseña guardada
en `/var/lib/openplex/initial-admin-password` (el archivo se borra cuando
cambias esa contraseña).

En la [guía de instalación](docs/es/installation.md) están todas las opciones,
las actualizaciones y cómo reconstruir un servidor perdido.

## Documentación

- [Instalación, actualizaciones y recuperación](docs/es/installation.md)
- [Webs: aplicaciones, configuración de PHP y SFTP](docs/es/websites.md)
- [OpenPlex para proveedores de hosting](docs/es/hosting-providers.md)
- [Licencia, extensiones y marca](docs/es/licensing.md)
- [Crear una extensión](docs/es/extensions.md)

## Compilar desde el código

Necesitas Go 1.26 o superior y Node.js 22 o superior.

```bash
make build   # panel (exportación estática de Next.js) + bin/openplexd
make check   # go vet, pruebas y compilación sin CGO
```

¿Sin Go instalado? Usa la imagen oficial:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26-bookworm make check
```

## Licencia

Copyright © 2026 Avakiam.

El núcleo de OpenPlex se distribuye bajo la
[GNU Affero General Public License v3.0 (solo esa versión)](LICENSE). Si
modificas OpenPlex y lo ofreces a otros, también a través de la red, tienes
que publicar tus cambios bajo la misma licencia.

Las extensiones que solo usan la interfaz de extensiones documentada pueden
tener cualquier licencia, también privativa y de pago: mira la
[excepción para extensiones de OpenPlex](LICENSE-EXCEPTION.md) (resumen en
español en [docs/es/licensing.md](docs/es/licensing.md)). OpenPlex ofrecerá
extensiones oficiales, unas gratuitas y de código abierto y otras de pago.

"OpenPlex" y sus logotipos son marcas de Avakiam: mira
[TRADEMARKS.md](TRADEMARKS.md).

## Seguridad

¿Has encontrado una vulnerabilidad? Avísanos en privado; mira [SECURITY.md](SECURITY.md).

## Contribuir

Los avisos de errores, ideas y correcciones de traducción son bienvenidos
como issues. Todavía no aceptamos contribuciones de código; mira
[CONTRIBUTING.md](CONTRIBUTING.md).
