# Instalación, actualizaciones y recuperación

[English](../en/installation.md) · **Español** · [Català](../ca/installation.md)

## Antes de empezar

- Un servidor limpio con **Ubuntu 22.04+** o **Debian 12+** (amd64 o arm64),
  acceso root y una IP pública. Ningún otro servicio debe usar los puertos 80
  y 443.
- Se recomiendan 2 GB de RAM o más; con 1 GB funcionan unas pocas webs
  pequeñas sin correo.
- Un nombre para el panel (por ejemplo `panel.example.com`) con un registro A
  (y AAAA si tienes IPv6) que apunte al servidor. Let's Encrypt lo necesita
  para emitir el certificado del panel.
- Si vas a alojar correo: un proveedor que permita tráfico saliente por el
  puerto 25, y el DNS inverso (PTR) de la IP del servidor apuntando al nombre
  del servidor de correo.

## Instalar

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | sudo bash -s -- \
  --panel-domain panel.example.com --acme-email tu@example.com
```

El instalador no hace preguntas y se puede volver a ejecutar sin riesgo:

1. instala Docker Engine desde el repositorio oficial de Docker (si falta),
   nftables y Fail2Ban;
2. descarga `openplexd`, comprueba su suma SHA-256 y lo instala en
   `/usr/local/bin`;
3. escribe `/etc/openplex/openplex.env` y el servicio systemd `openplexd`;
4. arranca el servicio, que crea la cuenta de administrador, el proxy y las
   reglas del cortafuegos.

### Opciones

| Opción | Qué hace |
|---|---|
| `--panel-domain HOST` | Sirve el panel en `https://HOST` a través del proxy |
| `--acme-email EMAIL` | Dirección de contacto para Let's Encrypt / ZeroSSL |
| `--version VERSIÓN` | Instala esa versión en lugar de la última |
| `--binary RUTA` | Instala un binario `openplexd` local en lugar de descargarlo |
| `--listen DIR` | Dirección directa del panel (por defecto `127.0.0.1:7080`, solo accesible desde el servidor) |
| `--no-firewall` | No toca el cortafuegos del servidor (sin reglas nftables ni Fail2Ban) |

## Primer acceso

Abre `https://panel.example.com` y entra como **`admin`**. La contraseña está
en `/var/lib/openplex/initial-admin-password`:

```bash
sudo cat /var/lib/openplex/initial-admin-password
```

Cámbiala desde tu perfil y activa la verificación en dos pasos. El archivo se
borra solo cuando cambias esa contraseña. Si alguna vez pierdes la contraseña
del administrador:

```bash
sudo openplexd reset-password admin
```

## Puertos

| Puerto | Uso |
|---|---|
| 22 | SSH (el cortafuegos siempre lo permite) |
| 80, 443 (TCP), 443 (UDP) | Webs, panel y webmail (proxy) |
| 25, 465, 587, 143, 993, 110, 995, 4190 | Correo, si está activado |
| 53 (TCP/UDP) | DNS, si el servidor DNS está activado |
| 2222 | SFTP para los archivos de las webs (el cortafuegos siempre lo permite; el instalador también lo abre en `ufw`) |
| 3306, 5432 | Acceso remoto a bases de datos, solo mientras alguna lo tiene activado y solo para las direcciones de su lista |

El cortafuegos se gestiona en **Firewall** dentro del panel. Se queda
apagado si `ufw` está activo, para que no se pisen las reglas.

Con IPv6 en el servidor (y Docker 27 o posterior), las webs, el panel, el
servidor DNS y el correo responden también por IPv6 y ven las direcciones
reales de los clientes. El correo saliente sigue prefiriendo IPv4: configura
también el DNS inverso de la dirección IPv6 del servidor antes de contar con
ella.

## Configuración

Los ajustes están en `/etc/openplex/openplex.env` (`CLAVE=valor`; reinicia el
servicio después de cambiarlos). Los más útiles:

| Variable | Por defecto | Qué hace |
|---|---|---|
| `OPENPLEX_PANEL_DOMAIN` | — | Nombre del panel |
| `OPENPLEX_ACME_EMAIL` | — | Dirección de contacto de los certificados |
| `OPENPLEX_DATA_DIR` | `/var/lib/openplex` | Todo el estado: base de datos, webs, claves, certificados |
| `OPENPLEX_LISTEN` | `127.0.0.1:7080` | Dirección directa del panel |
| `OPENPLEX_FIREWALL` | `true` | Gestionar el cortafuegos del servidor |
| `OPENPLEX_CAPTCHA` | `true` | `false` desactiva el captcha del inicio de sesión (por ejemplo tras poner una clave errónea) |
| `OPENPLEX_TENANT_NETWORKS` | `10.213.0.0/16` | Rango de direcciones para la red privada de cada cuenta (una /24 cada una) |
| `OPENPLEX_SFTP_LISTEN` | `:2222` | Servidor SFTP para los archivos de las webs (vacío lo desactiva) |
| `OPENPLEX_DB_REMOTE_MARIADB` | `:3306` | Acceso remoto a las bases de datos MariaDB (vacío lo desactiva) |
| `OPENPLEX_DB_REMOTE_POSTGRES` | `:5432` | Acceso remoto a las bases de datos PostgreSQL (vacío lo desactiva) |

## Alertas por correo

**Alertas** (administradores) envía un correo a las direcciones que elijas
cuando algo necesita atención. Lo comprueba cada minuto:

| Alerta | Cuándo |
|---|---|
| Falla una copia de seguridad | cada copia fallida, una vez |
| Una web está caída | tras dos minutos con error, y otra vez cuando se recupera |
| El disco está casi lleno | por encima del 90 % del disco de datos, y otra vez por debajo del 85 % |
| Una base de datos supera su límite de tamaño | cuando pasa a solo lectura, y otra vez cuando vuelve a estar por debajo (ver [bases de datos](databases.md#límites-de-tamaño)) |
| Un certificado no se renueva | cuando el de un nombre en uso está a punto de caducar (la renovación lleva tiempo fallando: suele ser el DNS o el puerto 80) |
| Nueva versión de OpenPlex | una vez por versión |
| Falla una actualización de OpenPlex | cuando una actualización se deshace o falla |

Un problema en curso se envía una vez, se recuerda cada día mientras dura y se
avisa cuando termina; reiniciar el panel no reenvía nada. Los correos están en
inglés, español o catalán.

Se envían mediante el servicio de correo de este servidor (si **Correo** está
activado) o mediante un servidor SMTP, como el de tu proveedor (STARTTLS, TLS o
ninguna; la contraseña se guarda cifrada). Con el correo local, usa un
remitente de un dominio de correo alojado aquí cuyo registro SPF autorice al
servidor, o los mensajes pueden acabar en spam. **Enviar un correo de prueba**
comprueba la configuración antes de guardarla; la página también muestra las
alertas de los últimos 90 días y por qué no se pudo enviar alguna.

## Gestionar el servicio

```bash
systemctl status openplexd
journalctl -u openplexd -f
sudo systemctl restart openplexd
```

## Actualizar

**Desde el panel** (OpenPlex 0.18 o posterior): **Actualizaciones** muestra la
versión instalada y, dos veces al día, busca una nueva versión estable en
GitHub; la entrada del menú muestra un punto cuando la hay. **Actualizar a …**
entonces:

1. descarga la versión y la comprueba: la suma de control y la firma hecha con
   la clave de versiones de Avakiam (una versión firmada de otra forma se
   rechaza);
2. detiene el panel aproximadamente un minuto (las webs, las bases de datos y
   el correo siguen funcionando), guarda una copia del binario actual y de su
   base de datos en `/var/lib/openplex/updates/`, instala la nueva versión y
   la arranca;
3. si la nueva versión no arranca correctamente en tres minutos, vuelve a
   poner sola el binario y la base de datos anteriores.

El resultado, y quién la inició, queda en el registro de auditoría. Desactiva
**Buscar actualizaciones automáticamente** si gestionas tú las versiones (la
comprobación solo se hará al pulsar **Comprobar ahora**).

**Con el instalador**: vuelve a ejecutarlo con las mismas opciones. Sustituye
el binario (comprobando también la firma) y conserva todos los datos; al
arrancar, el servicio migra su base de datos y actualiza los contenedores que
gestiona. Haz una copia antes.

## Copias de seguridad y reconstruir un servidor perdido

Configura las copias en **Copias de seguridad**: una carpeta local o un
bucket S3, una programación y cuántas copias conservar. **Guarda la clave de
recuperación que aparece ahí en un lugar seguro y fuera del servidor**: sin
ella no se pueden leer las copias.

La página también indica lo seguro que es el repositorio. Avisa cuando las
copias están en el mismo disco que los datos (la carpeta por defecto,
`/var/lib/openplex/backups`, siempre lo está) o en un almacenamiento S3 servido
por este mismo servidor: un solo fallo se llevaría a la vez las webs y sus
copias. Un segundo disco del servidor es mejor (sobrevive a un fallo de disco,
no a perder el servidor); un almacenamiento compatible con S3 en otro sitio, o
una carpeta de red (NFS, SMB, sshfs) montada en la carpeta de copias, las
mantiene fuera del servidor.

Para reconstruir un servidor que ya no existe:

1. Instala OpenPlex en el servidor nuevo con las mismas opciones.
2. Restaura el panel desde el repositorio de copias:

   ```bash
   sudo systemctl stop openplexd
   sudo OPENPLEX_RECOVERY_KEY='…' openplexd restore-panel --repo s3:https://s3.example.com/bucket/openplex
   sudo systemctl start openplexd
   ```

   Con S3, define también `AWS_ACCESS_KEY_ID` y `AWS_SECRET_ACCESS_KEY`. Si
   el repositorio es local o está montado, pasa su ruta a `--repo`.

3. Al arrancar, OpenPlex restaura las webs, bases de datos, correo y webmail
   de la misma copia. Apunta tu DNS a la IP nueva si ha cambiado (las zonas
   alojadas en OpenPlex se actualizan solas).

`openplexd restore-panel --help` muestra todas las opciones (`--snapshot`,
`--ipv4`, `--ipv6`, `--force`…).

## Desinstalar

```bash
sudo systemctl disable --now openplexd
sudo docker rm -f $(sudo docker ps -aq --filter label=openplex.managed=true)
sudo docker volume rm openplex-mail-data openplex-webmail-db
sudo docker network prune -f --filter label=openplex.managed=true
sudo rm -rf /var/lib/openplex /etc/openplex /usr/local/bin/openplexd /etc/systemd/system/openplexd.service
```

Esto borra todas las webs, bases de datos y buzones del servidor.
