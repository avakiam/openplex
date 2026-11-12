# Bases de dades

**Bases de dades** crea bases de dades MariaDB 11.8 i PostgreSQL 17 (cadascuna
amb el seu propi usuari i una contrasenya forta generada) i instàncies de Redis
7.4. Els webs hi arriben pels noms d'amfitrió de **Dades de connexió**
(`mariadb`, `postgres`, `opx-redis-<id>`); des d'internet estan tancades llevat
que activis l'accés remot. **Obre a Adminer** obre la base de dades a Adminer
amb la sessió ja iniciada, i **Còpies de seguretat** fa o restaura la còpia
d'una base de dades.

## Accés remot

Per treballar amb una base de dades MariaDB o PostgreSQL des del teu propi
ordinador (DBeaver, TablePlus, HeidiSQL, `mariadb`, `psql`…), obre **Accés
remot** al seu menú i indica les adreces IP o xarxes (CIDR, p. ex.
`203.0.113.0/24`) que s'hi poden connectar, fins a 20. **Afegeix la meva
adreça** afegeix aquella des de la qual et veu el panell. Deixa la llista
buida, o fes servir **Desactiva**, per tornar-la a tancar. Només ho pot canviar
el propietari de la base de dades (o un administrador o revenedor que gestioni
aquell compte).

Connecta't a l'adreça del servidor (o al nom del panell) al port 3306 per a
MariaDB o 5432 per a PostgreSQL, amb l'usuari i la contrasenya de la base de
dades i TLS (SSL) obligatori:

```bash
mariadb -h panel.example.com -P 3306 -u alice_shop -p --ssl-ca=openplex-databases.pem alice_shop
psql "host=panel.example.com port=5432 dbname=alice_shop user=alice_shop sslmode=verify-ca sslrootcert=openplex-databases.pem"
```

Com funciona i què el protegeix:

- És el mateix OpenPlex qui respon en aquests ports, i només mentre alguna base
  de dades d'aquell motor té l'accés remot activat. Comprova l'adreça del
  client abans de dir res: a una adreça que no és a cap llista se li tanca la
  connexió de seguida.
- Les connexions han d'usar TLS, amb el certificat propi d'OpenPlex
  (`/var/lib/openplex/db-proxy.pem`, vàlid per al nom del servidor, les seves
  adreces i el domini del panell). **Descarrega el certificat** el desa perquè
  el teu client el verifiqui (`--ssl-ca`, `sslrootcert`), o compara'n
  l'empremta SHA-256 amb la del diàleg. Un client que no demana TLS es rebutja
  amb un missatge que ho explica.
- Cada llista obre només la seva base de dades: OpenPlex llegeix l'usuari (i,
  a PostgreSQL, la base de dades) que demana el client i només el deixa passar
  si aquella base de dades permet la seva adreça. Després el servidor de bases
  de dades comprova la contrasenya com sempre.
- Amb el tallafoc del panell activat, el port s'obre només a les adreces de la
  llista. Si `ufw` és actiu, el diàleg avisa que manté el port tancat; un
  administrador el permet amb `ufw allow 3306/tcp` (o només des de la teva
  adreça: `ufw allow from 203.0.113.7 to any port 3306 proto tcp`).

Si un altre programa del servidor ja fa servir el port (un MariaDB o PostgreSQL
instal·lat a l'amfitrió), activar l'accés remot falla amb "address already in
use". Mou OpenPlex a un altre port amb `OPENPLEX_DB_REMOTE_MARIADB` /
`OPENPLEX_DB_REMOTE_POSTGRES` (p. ex. `:5433`) a `/etc/openplex/openplex.env` i
reinicia el servei; un valor buit desactiva l'accés remot a aquell motor. Redis
no té accés remot.
