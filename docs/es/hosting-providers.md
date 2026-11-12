# OpenPlex para proveedores de hosting

[English](../en/hosting-providers.md) · **Español** · [Català](../ca/hosting-providers.md)

OpenPlex está pensado para ofrecerse igual que los proveedores ofrecen Plesk
o cPanel: un servidor que viene con el panel listo, para clientes que quieren
alojar webs, correo y bases de datos sin administrar Linux. No necesita
servidor de licencias ni pagar por servidor, y una versión **sin modificar**
se puede ofrecer con el nombre OpenPlex (mira [licencia](licensing.md)).

## Lo que ya funciona

### Instalación en el primer arranque (cloud-init o script de post-instalación)

El instalador no hace preguntas, así que cualquier proveedor que ejecute un
script en un servidor nuevo puede entregar OpenPlex. Con cloud-init:

```yaml
#cloud-config
runcmd:
  - curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | bash -s -- --panel-domain "$(hostname -f)" --acme-email ops@provider.example
```

El nombre del propio servidor (por ejemplo el que asignas, como
`vps-1234.provider.example`) pasa a ser la dirección del panel, con su
certificado. Después el cliente entra como `admin` con la contraseña de
`/var/lib/openplex/initial-admin-password`.

La mayoría de proveedores, además, configuran en cada servidor:

- el **DNS inverso** (PTR) de sus IP → el nombre del servidor (necesario para el correo);
- el **puerto 25** abierto para el correo saliente, o una forma documentada de pedirlo;
- un **registro A/AAAA** para el nombre del servidor.

### Varios clientes en un servidor

Un servidor OpenPlex puede alojar muchas cuentas: administradores,
**revendedores** (que crean y gestionan a sus propios clientes dentro de sus
cuotas) y **clientes**. Las webs de cada cuenta se ejecutan en sus propios
contenedores, usuarios y red privada, con límites de memoria y CPU que solo
cambian los administradores.

### Automatización con la API

Todo lo que hace el panel pasa por la API HTTP en `/api/v1`, autenticada con
claves de API (se crean en la página **Claves API**). Un sistema de facturación
o aprovisionamiento puede crear usuarios con cuotas, webs, bases de datos y
buzones. La [referencia de la API](api.md) recoge todos los endpoints.

### Una plantilla con OpenPlex listo (modo imagen)

Para crear servidores a partir de tu propia imagen en vez de instalar en cada
uno, prepara la plantilla en un servidor **recién creado** con `--image`:

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | bash -s -- --image --acme-email ops@provider.example
```

Instala Docker, el cortafuegos y el servicio `openplexd`, y descarga y
construye las imágenes de contenedor (servidor web, bases de datos, DNS,
correo, PHP), pero **no** arranca OpenPlex: la plantilla no lleva secretos.
Opciones: `--php 8.2,8.3` construye esas versiones de PHP por adelantado (si
no, la de por defecto) y `--no-mail` deja fuera las imágenes de correo (cerca
de 1 GB). Se niega a ejecutarse en un servidor donde OpenPlex ya ha arrancado.

Después generaliza el sistema como sueles hacerlo (`cloud-init clean`, ID de
máquina, claves de host SSH), apágalo y haz la imagen. En su primer
arranque, cada servidor creado a partir de ella genera su propia clave
secreta, su cuenta de administrador y sus certificados. El panel responde en
el nombre de host del servidor (`OPENPLEX_PANEL_DOMAIN=auto`: el nombre
completo que tiene el sistema, o el que cloud-init pone en `/etc/hosts`), así
que da a los servidores un nombre completo como `vps-1234.provider.example`
con su registro DNS.

### Entregar el servidor

`openplexd login-link` muestra un enlace de acceso de un solo uso para la
cuenta de administrador, válido 24 horas (`--ttl 72h` para hasta una semana,
`--user NOMBRE` para otra cuenta). El cliente lo abre, entra una vez y llega a
la página de su cuenta para poner una contraseña propia. En el primer
arranque espera a que el panel cree la cuenta de administrador, así que
cloud-init puede ejecutarlo:

```yaml
#cloud-config
runcmd:
  - openplexd login-link --ttl 72h > /root/openplex-link
```

y tu sistema de aprovisionamiento lee el fichero (por SSH o con tu agente
invitado) y envía el enlace al cliente. Un enlace sirve una sola vez; no da
acceso a cuentas con verificación en dos pasos. La contraseña de
administrador sigue también en `/var/lib/openplex/initial-admin-password`.

### Actualizaciones

Las versiones van firmadas, y el panel comprueba la firma antes de instalar
una. Por defecto busca una versión nueva en GitHub dos veces al día y el
administrador la instala desde la página **Actualizaciones**.

Para elegir qué versión reciben tus servidores (por ejemplo, después de
probarla), sirve tu propio fichero y pasa `--update-url https://…` al
instalador (`OPENPLEX_UPDATE_URL` en `/etc/openplex/openplex.env`). El fichero
tiene el formato de la API de versiones de GitHub (`tag_name`, `name`,
`body`, `html_url`, `published_at` y `assets` con `name` y
`browser_download_url`): sirve una copia de la respuesta de
`https://api.github.com/repos/avakiam/openplex/releases/tags/vX.Y.Z`, y sus
direcciones de descarga pueden apuntar a tu propio espejo. Las firmas se
siguen comprobando con las claves de versión de OpenPlex, así que tu fichero
elige entre las versiones oficiales; no puede cambiarlas.

## Previsto para proveedores

- **Integraciones de facturación** (por ejemplo, un módulo de WHMCS).
- **Extensiones oficiales**, gratuitas y de pago, y una interfaz de
  extensiones abierta para las tuyas.

¿Te interesa ofrecer OpenPlex? Abre un issue en el
[repositorio principal](https://github.com/avakiam/openplex).
