# OpenPlex per a proveïdors d'allotjament

[English](../en/hosting-providers.md) · [Español](../es/hosting-providers.md) · **Català**

OpenPlex està pensat per oferir-se igual que els proveïdors ofereixen Plesk o
cPanel: un servidor que ve amb el tauler a punt, per a clients que volen
allotjar webs, correu i bases de dades sense administrar Linux. No necessita
servidor de llicències ni pagar per servidor, i una versió **sense
modificar** es pot oferir amb el nom OpenPlex (mira [llicència](licensing.md)).

## El que ja funciona

### Instal·lació a la primera arrencada (cloud-init o script de post-instal·lació)

L'instal·lador no fa preguntes, així que qualsevol proveïdor que executi un
script en un servidor nou pot lliurar OpenPlex. Amb cloud-init:

```yaml
#cloud-config
runcmd:
  - curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | bash -s -- --panel-domain "$(hostname -f)" --acme-email ops@provider.example
```

El nom del mateix servidor (per exemple el que assignes, com ara
`vps-1234.provider.example`) passa a ser l'adreça del tauler, amb el seu
certificat. Després el client entra com a `admin` amb la contrasenya de
`/var/lib/openplex/initial-admin-password`.

La majoria de proveïdors, a més, configuren a cada servidor:

- el **DNS invers** (PTR) de les seves IP → el nom del servidor (necessari per al correu);
- el **port 25** obert per al correu sortint, o una manera documentada de demanar-lo;
- un **registre A/AAAA** per al nom del servidor.

### Diversos clients en un servidor

Un servidor OpenPlex pot allotjar molts comptes: administradors,
**revenedors** (que creen i gestionen els seus propis clients dins de les
seves quotes) i **clients**. Els webs de cada compte s'executen en els seus
propis contenidors, usuaris i xarxa privada, amb límits de memòria i CPU que
només canvien els administradors.

### Automatització amb l'API

Tot el que fa el tauler passa per l'API HTTP a `/api/v1`, autenticada amb
claus d'API (es creen a la pàgina **Claus API**). Un sistema de facturació o
d'aprovisionament pot crear usuaris amb quotes, webs, bases de dades i
bústies. La [referència de l'API](api.md) recull tots els endpoints.

### WHMCS

El [mòdul d'OpenPlex per a WHMCS](https://github.com/avakiam/openplex-whmcs) crea el compte (amb els
límits del producte i una web per al domini de la comanda) quan es paga la
comanda, el suspèn quan una factura venç —les seves webs queden fora de
línia—, el reactiva, el dona de baixa amb les seves webs i bases de dades,
canvia el seu pla i la seva contrasenya, i afegeix a l'àrea de client un botó
**Log in to OpenPlex**. Es descarrega de la pàgina de versions del mòdul; el
seu README n'explica la configuració (en anglès).

### Una plantilla amb OpenPlex a punt (mode imatge)

Per crear servidors a partir de la teva pròpia imatge en lloc d'instal·lar a
cadascun, prepara la plantilla en un servidor **acabat de crear** amb
`--image`:

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | bash -s -- --image --acme-email ops@provider.example
```

Instal·la Docker, el tallafoc i el servei `openplexd`, i descarrega i
construeix les imatges de contenidor (servidor web, bases de dades, DNS,
correu, PHP), però **no** arrenca OpenPlex: la plantilla no porta secrets.
Opcions: `--php 8.2,8.3` construeix aquestes versions de PHP per endavant (si
no, la de per defecte) i `--no-mail` deixa fora les imatges de correu (prop
d'1 GB). Es nega a executar-se en un servidor on OpenPlex ja ha arrencat.

Després generalitza el sistema com acostumes a fer (`cloud-init clean`, ID de
màquina, claus d'amfitrió SSH), apaga'l i fes la imatge. A la primera
arrencada, cada servidor creat a partir d'ella genera la seva pròpia clau
secreta, el seu compte d'administrador i els seus certificats. El tauler
respon al nom d'amfitrió del servidor (`OPENPLEX_PANEL_DOMAIN=auto`: el nom
complet que té el sistema, o el que cloud-init posa a `/etc/hosts`), així que
dona als servidors un nom complet com `vps-1234.provider.example` amb el seu
registre DNS.

### Lliurar el servidor

`openplexd login-link` mostra un enllaç d'accés d'un sol ús per al compte
d'administrador, vàlid 24 hores (`--ttl 72h` per a fins a una setmana,
`--user NOM` per a un altre compte). El client l'obre, entra una vegada i
arriba a la pàgina del seu compte per posar una contrasenya pròpia. A la
primera arrencada espera que el tauler creï el compte d'administrador, així
que cloud-init el pot executar:

```yaml
#cloud-config
runcmd:
  - openplexd login-link --ttl 72h > /root/openplex-link
```

i el teu sistema d'aprovisionament llegeix el fitxer (per SSH o amb el teu
agent convidat) i envia l'enllaç al client. Un enllaç serveix una sola
vegada; no dona accés a comptes amb verificació en dos passos. La
contrasenya d'administrador continua també a
`/var/lib/openplex/initial-admin-password`.

### Actualitzacions

Les versions van signades, i el tauler comprova la signatura abans
d'instal·lar-ne una. Per defecte busca una versió nova a GitHub dues vegades
al dia i l'administrador la instal·la des de la pàgina **Actualitzacions**.

Per triar quina versió reben els teus servidors (per exemple, després de
provar-la), serveix el teu propi fitxer i passa `--update-url https://…` a
l'instal·lador (`OPENPLEX_UPDATE_URL` a `/etc/openplex/openplex.env`). El
fitxer té el format de l'API de versions de GitHub (`tag_name`, `name`,
`body`, `html_url`, `published_at` i `assets` amb `name` i
`browser_download_url`): serveix una còpia de la resposta de
`https://api.github.com/repos/avakiam/openplex/releases/tags/vX.Y.Z`, i les
seves adreces de baixada poden apuntar al teu propi mirall. Les signatures es
continuen comprovant amb les claus de versió d'OpenPlex, així que el teu
fitxer tria entre les versions oficials; no les pot canviar.

## Previst per a proveïdors

- **Altres sistemes de facturació** (Blesta, HostBill…).
- **Extensions oficials**, gratuïtes i de pagament, i una interfície
  d'extensions oberta per a les teves.

T'interessa oferir OpenPlex? Obre un issue al
[repositori principal](https://github.com/avakiam/openplex).
