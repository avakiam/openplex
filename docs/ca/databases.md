# Bases de dades

**Bases de dades** crea bases de dades MariaDB 11.8 i PostgreSQL 17 (cadascuna
amb el seu propi usuari i una contrasenya forta generada) i instàncies de Redis
7.4. Els webs hi arriben pels noms d'amfitrió de **Dades de connexió**
(`mariadb`, `postgres`, `opx-redis-<id>`); des d'internet estan tancades llevat
que activis l'accés remot. **Obre a Adminer** obre la base de dades a Adminer
amb la sessió ja iniciada, i **Còpies de seguretat** fa o restaura la còpia
d'una base de dades.

## Límits de mida

Els administradors, i els revenedors per als seus clients, poden limitar quant
creixen les bases de dades:

- **Mida de cada base de dades (MB)** a la configuració del compte
  (**Usuaris**) s'aplica a totes les seves bases de dades MariaDB i PostgreSQL.
- **Límit de mida** al menú d'una base de dades li dona un límit propi (0
  torna al del compte).

Un revenedor amb un límit propi no pot donar més als seus clients ni treure'ls
el límit. Les instàncies de Redis mantenen el seu límit de memòria fix.

**Bases de dades** mostra la mida de cada base de dades davant del seu límit.
Les mides es comproven cada cinc minuts (i just després de canviar un límit).
Una base de dades per sobre del seu límit passa a **només lectura** per a les
seves aplicacions: la poden llegir però no hi poden desar res, així que els
seus webs poden donar errors quan ho intentin. El propietari la recupera
esborrant-ne dades (o demanant que li pugin el límit): a la següent
comprovació per sota del límit torna a admetre escriptures. Amb **Alertes**
activades, els administradors reben un correu quan una base de dades supera el
seu límit i un altre quan torna a estar-ne per sota.

Què vol dir només lectura a cada motor:

- **MariaDB**: l'usuari conserva només SELECT, DELETE, DROP, LOCK TABLES i
  SHOW VIEW, i es tanquen les seves connexions obertes. Esborrar files, buidar
  taules (`TRUNCATE`) o eliminar-les allibera espai; la mida compta el que
  contenen les taules, així que les files esborrades compten quan MariaDB les
  ha netejat, normalment a la següent comprovació.
- **PostgreSQL**: les sessions noves comencen en només lectura
  (`default_transaction_read_only`) i es tanquen les obertes. Les aplicacions
  deixen d'escriure però, com a propietari de la base de dades, el seu usuari
  encara ho pot desactivar expressament en una sessió, així que és un fre per
  a les aplicacions més que una barrera infranquejable. La mida és l'espai que
  ocupa al disc: després d'esborrar files, executa `VACUUM FULL` sobre la
  taula (en una sessió amb `SET default_transaction_read_only = off`) per
  alliberar-lo.

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
