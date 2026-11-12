# Databases

**Databases** creates MariaDB 11.8 and PostgreSQL 17 databases (each with its
own user and a strong generated password) and Redis 7.4 instances. Websites
reach them by the host names shown under **Connection details** (`mariadb`,
`postgres`, `opx-redis-<id>`); from the internet they are closed unless you
switch on remote access. **Open in Adminer** opens the database in Adminer
already logged in, and **Backups** backs up or restores one database.

## Size limits

Administrators, and resellers for their customers, can limit how big
databases grow:

- **Size of each database (MB)** in the account's settings (**Users**)
  applies to every MariaDB and PostgreSQL database of that account.
- **Size limit** in a database's menu gives that database its own limit
  instead (0 goes back to the account's).

A reseller with a limit of its own cannot give its customers more, nor take the
limit away. Redis instances keep their fixed memory limit.

**Databases** shows each database's size against its limit. Sizes are checked
every five minutes (and right after a limit changes). A database over its limit
becomes **read-only** for its applications: they can read it but not save
anything, so its websites may show errors when they try. The owner gets it
back by deleting data (or by having the limit raised): at the next check under
the limit it is writable again. With **Alerts** on, administrators get an
e-mail when a database goes over its limit, and another when it is back under.

What read-only means for each engine:

- **MariaDB**: the user keeps only SELECT, DELETE, DROP, LOCK TABLES and SHOW
  VIEW, and its open connections are closed. Deleting rows, emptying tables
  (`TRUNCATE`) or dropping tables frees space; the size counts what the tables
  hold, so deleted rows count once MariaDB has cleared them, usually by the
  next check.
- **PostgreSQL**: new sessions start read-only (`default_transaction_read_only`)
  and the open ones are closed. Applications stop writing, but, as the owner of
  the database, its user can still turn that off for a session on purpose, so
  this is a stop for applications rather than a hard barrier. The size is the
  space the database takes on disk: after deleting rows, run `VACUUM FULL` on
  the table (in a session with `SET default_transaction_read_only = off`) to
  give the space back.

## Remote access

To work on a MariaDB or PostgreSQL database from your own computer (DBeaver,
TablePlus, HeidiSQL, `mariadb`, `psql`…), open **Remote access** in its menu
and list the IP addresses or networks (CIDR, e.g. `203.0.113.0/24`) that may
connect, up to 20. **Add my address** adds the one the panel sees you from.
Leave the list empty, or use **Turn off**, to close it again. Only the owner of
the database (or an administrator or reseller who manages that account) can
change it.

Connect to the server's own address (or the panel's host name) on port 3306
for MariaDB or 5432 for PostgreSQL, with the database's user and password, and
TLS (SSL) required:

```bash
mariadb -h panel.example.com -P 3306 -u alice_shop -p --ssl-ca=openplex-databases.pem alice_shop
psql "host=panel.example.com port=5432 dbname=alice_shop user=alice_shop sslmode=verify-ca sslrootcert=openplex-databases.pem"
```

How it works and what protects it:

- OpenPlex itself answers on those ports, and only while some database of that
  engine has remote access on. It checks the client's address before saying
  anything: an address on no list gets the connection closed straight away.
- Connections must use TLS, with OpenPlex's own certificate
  (`/var/lib/openplex/db-proxy.pem`, valid for the server's host name,
  addresses and panel domain). **Download certificate** saves it so your
  client can verify it (`--ssl-ca`, `sslrootcert`), or compare its SHA-256
  fingerprint with the one in the dialog. A client that does not ask for TLS
  is refused with a message saying so.
- Each list opens only its own database: OpenPlex reads the user (and, for
  PostgreSQL, the database) the client asks for and lets it through only if
  that database allows the client's address. The password is then checked by
  the database server as usual.
- With the panel firewall on, the port is opened only to the addresses listed.
  If `ufw` is active, the dialog warns that it keeps the port closed; an
  administrator allows it with `ufw allow 3306/tcp` (or only from your address:
  `ufw allow from 203.0.113.7 to any port 3306 proto tcp`).

If another program on the server already uses the port (a MariaDB or
PostgreSQL installed on the host), switching remote access on fails with
"address already in use". Move OpenPlex to another port with
`OPENPLEX_DB_REMOTE_MARIADB` / `OPENPLEX_DB_REMOTE_POSTGRES` (e.g. `:5433`) in
`/etc/openplex/openplex.env` and restart the service; an empty value turns
remote access to that engine off. Redis has no remote access.
