# Webs

[English](../en/websites.md) · **Español** · [Català](../ca/websites.md)

Todo sobre alojar una web: crearla, instalar una aplicación, cambiar su
configuración de PHP y acceder a sus archivos por SFTP.

## Crear una web

**Sitios web y dominios → Añadir sitio web.** Escribe el nombre de host
(apunta antes su registro DNS A/AAAA al servidor, para que se pueda emitir el
certificado HTTPS), elige el propietario y cómo funciona:

| Entorno | Para |
|---|---|
| Estático | Archivos HTML, CSS y JavaScript, sin código de servidor |
| PHP 7.4 – 8.4 | WordPress, Joomla, Drupal, Laravel y cualquier código PHP |
| Node.js, Python, Go | Tu propia aplicación: indica el comando de arranque; escucha en `$PORT` |

Cada web funciona en su propio contenedor, con su propio usuario del sistema y
sus propios límites de memoria y CPU (solo los administradores los cambian).
Sus archivos están en `/var/lib/openplex/sites/<nombre>/`; el servidor web
sirve la **raíz de documentos**, `public/` por defecto.

Los subdominios (`blog.example.com`) son webs propias. Los **alias** son
nombres adicionales que muestran la misma web (`www.example.com`).

## Aplicaciones: WordPress, Joomla, Drupal

Al crear una web, elige una **Aplicación**. También puedes instalarla después
en una web vacía, desde **Aplicación → Instalar una aplicación** en su resumen.

1. Indica el título del sitio, el usuario y el correo del administrador, y su
   contraseña (se genera una: cópiala, OpenPlex no la guarda). WordPress y
   Drupal se pueden instalar en inglés, español o catalán; Joomla en inglés
   (añade idiomas desde su administración).
2. OpenPlex pasa la web a PHP, en una versión que la aplicación admita
   (Joomla 6 y Drupal 11 necesitan PHP 8.3 o posterior), y le crea una base de
   datos MariaDB, que cuenta en la cuota de bases de datos del propietario.
3. Se descarga la última versión desde el propio proyecto y se instala en la
   raíz de documentos. Tarda uno o dos minutos; la tarjeta **Aplicación**
   muestra el progreso y después la versión, la base de datos y un enlace a la
   administración.

La raíz de documentos debe estar vacía: el instalador nunca sobrescribe
archivos. Si una instalación falla, borra los archivos que escribió y su base
de datos, y la tarjeta explica por qué; puedes volver a intentarlo. Solo el
propietario de la web (y quienes gestionan esa cuenta) pueden instalar
aplicaciones, porque la base de datos se crea a su nombre.

WordPress puede empezar en inglés unos días después de una versión nueva,
hasta que se publique la traducción; luego la ofrece en **Escritorio →
Actualizaciones**.

### Actualizar una aplicación

La tarjeta **Aplicación** muestra lo que se puede actualizar: la propia
aplicación y, en WordPress, sus plugins y temas; en Drupal, sus módulos.
OpenPlex lo comprueba una vez al día; **Comprobar ahora** lo comprueba al
momento. **Actualizar** te deja elegir qué actualizar y después:

1. hace una copia de seguridad de los archivos de la web y de la base de datos
   de la aplicación (aparece en **Copias de seguridad** marcada *antes de una
   actualización*); si la copia falla, no se actualiza nada;
2. actualiza con las herramientas de la propia aplicación: WP-CLI en WordPress
   (con la actualización de la base de datos y las traducciones), la línea de
   comandos de Joomla en Joomla, Composer y Drush en Drupal (Drush se añade al
   proyecto la primera vez; mientras tanto la web está en modo mantenimiento,
   y si Composer falla se vuelve a dejar el código anterior);
3. comprueba que la web sigue respondiendo. Si da un error de servidor, la
   tarjeta indica que la actualización falló: restaura la copia desde la
   pestaña **Copias de seguridad** de la web.

Notas:

- Las extensiones de Joomla se actualizan desde su administración (**Sistema →
  Actualizar → Extensiones**); la tarjeta las muestra.
- Drupal se actualiza dentro de las versiones que permite su `composer.json`:
  no se ofrece una versión mayor nueva ni un módulo fijado a una versión
  exacta.
- Para actualizar, la web debe estar en marcha y el servidor necesita acceso a
  Internet; suele tardar menos de un minuto (unos minutos en Drupal).
- Solo el propietario de la web (y quien gestiona esa cuenta) puede comprobar
  y actualizar, igual que al instalar. También puedes seguir actualizando desde
  la administración de la aplicación.

### Comprobaciones de seguridad

La parte **Seguridad** de la tarjeta **Aplicación** muestra lo que debilita
la web; se comprueba junto con las actualizaciones (cada día o con
**Comprobar ahora**):

| Aplicación | Comprobaciones (✓ = OpenPlex lo puede corregir) |
|---|---|
| WordPress | editor de temas y plugins activado ✓, modo de depuración activado ✓, XML-RPC activado ✓, `wp-config.php` legible por otros usuarios ✓, un administrador llamado `admin`, plugins inactivos |
| Joomla | modo de depuración activado ✓, errores de PHP visibles ✓, HTTPS no obligatorio (si la web tiene HTTPS) ✓, carpeta `installation` olvidada ✓, `configuration.php` legible por otros usuarios ✓, un usuario llamado `admin` |
| Drupal | paquetes con vulnerabilidades conocidas (`composer audit`), sin nombres de host de confianza ✓, `update.php` abierto a cualquiera ✓, errores de PHP visibles ✓, `settings.php` modificable ✓, el administrador principal llamado `admin` |
| Todas las webs | HTTPS no obligatorio, una versión de PHP sin correcciones de seguridad (7.4, 8.0, 8.1), errores de PHP mostrados en las páginas |

Marca los problemas que quieras corregir y pulsa **Corregir los problemas
seleccionados**. Las correcciones son las habituales: `DISALLOW_FILE_EDIT` y
`WP_DEBUG` en `wp-config.php`, un pequeño plugin obligatorio que responde a
XML-RPC con un 403 (borra `wp-content/mu-plugins/openplex-disable-xmlrpc.php`
para volver a activarlo), la configuración global de Joomla, líneas marcadas
`// OpenPlex security` al final del `settings.php` de Drupal (los hosts de
confianza son el nombre de la web y sus alias), y permisos privados o de solo
lectura. Si después la web deja de responder, el archivo de configuración
vuelve a quedar como estaba. El resto (una cuenta `admin`, los ajustes de la
propia web) los cambias tú.

Las instalaciones nuevas ya no proponen `admin` como nombre del
administrador.

### Copias de staging

Una copia de staging es una copia aparte de una web, en un subdominio, para
probar actualizaciones, plugins o cambios antes de que los vean los
visitantes.

**Crear una copia de staging** (en la tarjeta **Aplicación**) crea la web
`staging.<tu web>` (u otro nombre que elijas) con el mismo entorno, versión y
configuración de PHP, una base de datos nueva, y una copia de los archivos y
de la base de datos de la web en producción. Después la copia se adapta: su
configuración apunta a su propia base de datos, se reescriben las direcciones
de WordPress, y WordPress y Joomla piden a los buscadores que no la indexen.
La web en producción no se modifica.

El nombre de staging debe apuntar al servidor, como la web en producción (ya
lo hace si el panel gestiona tu DNS), para que la copia sea accesible y
obtenga su certificado HTTPS. Cuenta como una web y una base de datos de la
cuenta.

En la copia, **Enviar a `<web en producción>`** la vuelve a copiar sobre la
web en producción. Elige:

- **Archivos**: código, plugins, temas y archivos subidos. Se conserva el
  archivo de configuración de producción (`wp-config.php`,
  `configuration.php` o `sites/default/settings.php`), así que la web en
  producción sigue usando su propia base de datos.
- **Base de datos**: contenido y ajustes. Lo que haya cambiado en la web en
  producción desde que se hizo la copia (comentarios, pedidos, usuarios
  nuevos…) se pierde.

Antes de enviar, OpenPlex hace una copia de seguridad de la web en producción
y de su base de datos (marcada *antes de enviar un staging* en **Copias de
seguridad**); si falla, no se cambia nada. Después, WordPress recupera sus
direcciones y su ajuste para buscadores, se reconstruyen las cachés de Drupal
y OpenPlex comprueba que la web en producción sigue respondiendo; si no,
restaura esa copia.

Para tener una copia nueva, borra la web de staging y crea otra copia. Su
base de datos se queda hasta que la borres en **Bases de datos** (borrar la
web en producción también borra sus subdominios, incluidas las copias de
staging).

## Configuración de PHP

En una web PHP, **Configuración → Configuración de PHP** cambia estos valores.
Los campos vacíos mantienen el valor por defecto (en gris).

| Ajuste | Por defecto | Límites |
|---|---|---|
| Memoria por script (`memory_limit`) | 256M | de 32M a la memoria de la web |
| Subida de archivo máxima (`upload_max_filesize`) | 64M | hasta 2048M |
| Envío de formulario máximo (`post_max_size`) | 64M | hasta 2048M, al menos el tamaño de subida |
| Tiempo máximo de script (`max_execution_time`) | 60 s | 1 – 3600 |
| Tiempo para leer una petición (`max_input_time`) | 60 s | 1 – 3600 |
| Campos de formulario por petición (`max_input_vars`) | 3000 | 100 – 100000 |
| Duración de la sesión (`session.gc_maxlifetime`) | 1440 s | 60 – 604800 |
| Mostrar los errores en las páginas (`display_errors`) | No | Sí / No |
| Abrir URL remotas como archivos (`allow_url_fopen`) | Sí | Sí / No |
| Zona horaria (`date.timezone`) | UTC | p. ej. `Europe/Madrid` |
| Funciones desactivadas (`disable_functions`) | ninguna | nombres separados por comas |

Al guardar se reinicia el contenedor de la web (unos segundos). Los valores se
aplican a la web, a sus tareas programadas y a su terminal web por igual.
Deja **Mostrar los errores** desactivado en una web en producción: los
mensajes de error pueden revelar rutas y configuración a los visitantes.

## Archivos por SFTP

Además del gestor de archivos (pestaña **Archivos**), cada usuario puede
acceder a los archivos de sus webs con un cliente SFTP como FileZilla, WinSCP
o Cyberduck. Los datos están en **Claves API y SSH → Acceso SFTP**:

| | |
|---|---|
| Host | la dirección del panel (o del servidor) |
| Puerto | 2222 |
| Usuario | tu usuario del panel |
| Contraseña | una **contraseña SFTP** creada en esa página, o una de tus **claves SSH** |

La contraseña SFTP es distinta de la del panel y no puede ser la misma: los
clientes SFTP la guardan, y nunca sirve para entrar en el panel. Para usar una
clave SSH, añade su parte pública en **Claves SSH**, en la misma página.

Al entrar verás una carpeta por cada web cuyos archivos puedes gestionar: tus
webs, las de las cuentas que gestionas y las webs compartidas contigo con el
área **Archivos** (de solo lectura si lo es el acceso compartido). Los
archivos nuevos pertenecen al usuario de la web, como con el gestor de
archivos. No se pueden crear enlaces, mover archivos entre webs ni ejecutar
comandos.

En la primera conexión el cliente muestra la huella de la clave del servidor:
compárala con la de la tarjeta **Acceso SFTP**. Los intentos de inicio de
sesión fallidos repetidos desde una dirección la bloquean (Fail2Ban), igual
que en el panel.

El servidor escucha en el puerto 2222 (`OPENPLEX_SFTP_LISTEN`, ver
[instalación](installation.md#configuración)); el cortafuegos del panel lo
mantiene abierto. En un servidor que usa ufw en su lugar, lo abre el
instalador; si ufw aún lo bloquea (por ejemplo, tras actualizar desde el
panel), la tarjeta **Acceso SFTP** lo avisa, y un administrador ejecuta
`ufw allow 2222/tcp` en el servidor.
