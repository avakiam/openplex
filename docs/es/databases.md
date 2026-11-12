# Bases de datos

**Bases de datos** crea bases de datos MariaDB 11.8 y PostgreSQL 17 (cada una
con su propio usuario y una contraseña fuerte generada) e instancias de Redis
7.4. Las webs llegan a ellas por los nombres de host de **Datos de conexión**
(`mariadb`, `postgres`, `opx-redis-<id>`); desde internet están cerradas salvo
que actives el acceso remoto. **Abrir en Adminer** abre la base de datos en
Adminer con la sesión ya iniciada, y **Copias de seguridad** hace o restaura la
copia de una base de datos.

## Acceso remoto

Para trabajar con una base de datos MariaDB o PostgreSQL desde tu propio
ordenador (DBeaver, TablePlus, HeidiSQL, `mariadb`, `psql`…), abre **Acceso
remoto** en su menú e indica las direcciones IP o redes (CIDR, p. ej.
`203.0.113.0/24`) que pueden conectarse, hasta 20. **Añadir mi dirección**
añade aquella desde la que te ve el panel. Deja la lista vacía, o usa
**Desactivar**, para volver a cerrarla. Solo puede cambiarlo el propietario de
la base de datos (o un administrador o revendedor que gestione esa cuenta).

Conéctate a la dirección del servidor (o al nombre del panel) en el puerto 3306
para MariaDB o 5432 para PostgreSQL, con el usuario y la contraseña de la base
de datos y TLS (SSL) obligatorio:

```bash
mariadb -h panel.example.com -P 3306 -u alice_shop -p --ssl-ca=openplex-databases.pem alice_shop
psql "host=panel.example.com port=5432 dbname=alice_shop user=alice_shop sslmode=verify-ca sslrootcert=openplex-databases.pem"
```

Cómo funciona y qué lo protege:

- Es el propio OpenPlex el que responde en esos puertos, y solo mientras alguna
  base de datos de ese motor tiene el acceso remoto activado. Comprueba la
  dirección del cliente antes de decir nada: a una dirección que no está en
  ninguna lista se le cierra la conexión enseguida.
- Las conexiones deben usar TLS, con el certificado propio de OpenPlex
  (`/var/lib/openplex/db-proxy.pem`, válido para el nombre del servidor, sus
  direcciones y el dominio del panel). **Descargar el certificado** lo guarda
  para que tu cliente lo verifique (`--ssl-ca`, `sslrootcert`), o compara su
  huella SHA-256 con la del diálogo. Un cliente que no pide TLS se rechaza con
  un mensaje que lo explica.
- Cada lista abre solo su propia base de datos: OpenPlex lee el usuario (y, en
  PostgreSQL, la base de datos) que pide el cliente y solo lo deja pasar si esa
  base de datos permite su dirección. Después el servidor de bases de datos
  comprueba la contraseña como siempre.
- Con el cortafuegos del panel activado, el puerto se abre solo a las
  direcciones de la lista. Si `ufw` está activo, el diálogo avisa de que
  mantiene el puerto cerrado; un administrador lo permite con
  `ufw allow 3306/tcp` (o solo desde tu dirección:
  `ufw allow from 203.0.113.7 to any port 3306 proto tcp`).

Si otro programa del servidor ya usa el puerto (un MariaDB o PostgreSQL
instalado en el host), activar el acceso remoto falla con "address already in
use". Mueve OpenPlex a otro puerto con `OPENPLEX_DB_REMOTE_MARIADB` /
`OPENPLEX_DB_REMOTE_POSTGRES` (p. ej. `:5433`) en `/etc/openplex/openplex.env` y
reinicia el servicio; un valor vacío desactiva el acceso remoto a ese motor.
Redis no tiene acceso remoto.
