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
bústies. Abans de la 1.0 es publicarà una referència de l'API.

## Important: no facis una imatge d'un servidor ja inicialitzat

En la seva primera arrencada `openplexd` genera la clau secreta del servidor,
el compte d'administrador amb la seva contrasenya i els certificats. Si fas
una instantània d'un servidor on OpenPlex ja ha arrencat i la fas servir com
a plantilla, **tots els servidors creats a partir d'ella comparteixen
aquests secrets**. Fins que existeixi el mode imatge descrit a sota,
instal·la a la primera arrencada (com a dalt).

## Previst per a proveïdors

- **Mode imatge:** preparar una plantilla amb OpenPlex i les seves imatges de
  contenidors ja baixades però sense inicialitzar, perquè cada servidor nou
  acabi de configurar-se (secrets, nom, IP) en segons en arrencar.
- **Lliurament de l'accés:** un enllaç d'accés d'un sol ús, o una contrasenya
  d'administrador passada per cloud-init, per enviar-la al client per correu.
- **Versions signades** i un canal d'actualitzacions.
- **Referència de l'API** i integracions de facturació (per exemple un mòdul
  per a WHMCS).
- **Extensions oficials**, gratuïtes i de pagament, i una interfície
  d'extensions oberta per crear les teves.

T'interessa oferir OpenPlex? Obre un issue al
[repositori principal](https://github.com/avakiam/openplex).
