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
buzones. Antes de la 1.0 se publicará una referencia de la API.

## Importante: no hagas una imagen de un servidor ya inicializado

En su primer arranque `openplexd` genera la clave secreta del servidor, la
cuenta de administrador con su contraseña y los certificados. Si haces una
instantánea de un servidor donde OpenPlex ya ha arrancado y la usas como
plantilla, **todos los servidores creados a partir de ella comparten esos
secretos**. Hasta que exista el modo imagen descrito abajo, instala en el
primer arranque (como arriba).

## Previsto para proveedores

- **Modo imagen:** preparar una plantilla con OpenPlex y sus imágenes de
  contenedores ya descargadas pero sin inicializar, para que cada servidor
  nuevo termine de configurarse (secretos, nombre, IP) en segundos al
  arrancar.
- **Entrega del acceso:** un enlace de acceso de un solo uso, o una
  contraseña de administrador pasada por cloud-init, para enviársela al
  cliente por correo.
- **Versiones firmadas** y un canal de actualizaciones.
- **Referencia de la API** e integraciones de facturación (por ejemplo un
  módulo para WHMCS).
- **Extensiones oficiales**, gratuitas y de pago, y una interfaz de
  extensiones abierta para crear las tuyas.

¿Te interesa ofrecer OpenPlex? Abre un issue en el
[repositorio principal](https://github.com/avakiam/openplex).
