# Crear una extensió

[English](../en/extensions.md) · [Español](../es/extensions.md) · **Català**

Una extensió d'OpenPlex afegeix funcions al tauler: pàgines pròpies al menú,
pestanyes a la pàgina d'un web, configuració i reaccions al que passa al
servidor (es crea un web, canvia un registre DNS…). Qualsevol en pot crear
una, en qualsevol llenguatge, i distribuir-la gratis o de pagament, oberta o
tancada: mira [llicència](licensing.md).

Hi ha un exemple complet per copiar a
[`examples/extension-hello`](../../examples/extension-hello) (llicència 0BSD:
fes-lo servir com vulguis).

## Com funciona

- Una extensió és un **contenidor** més un **manifest**
  (`openplex-extension.json`), distribuïts en un paquet (`.opx`).
- OpenPlex executa el contenidor amb el seu propi usuari, sense privilegis
  (capabilities), amb el sistema de fitxers arrel en només lectura i en la
  seva pròpia xarxa (amb Internet, sense res més del servidor). Conserva
  `/data` entre reinicis i actualitzacions.
- L'extensió escolta HTTP a `$PORT`. OpenPlex li envia **esdeveniments** i
  li passa les visites a les seves **pàgines** dels usuaris que han iniciat
  sessió.
- L'extensió crida l'**API d'OpenPlex** per un sòcol unix,
  `$OPENPLEX_SOCKET` (`/run/openplex/panel.sock`), limitada als **permisos**
  que va acceptar l'administrador en instal·lar-la.

```
 navegador ──▶ OpenPlex ──(pàgines, esdeveniments: HTTP :$PORT)──▶ contenidor de l'extensió
                  ▲                                                         │
                  └─────────── API per /run/openplex/panel.sock ────────────┘
```

## Començar ràpid

```bash
cd examples/extension-hello
docker build -t openplex-examples/hello:1.0.0 .
mkdir -p images && docker save -o images/amd64.tar openplex-examples/hello:1.0.0
openplexd ext pack .            # → example.hello-1.0.0.opx
```

Després, al tauler: **Extensions → Pujar extensió**, revisa-la i
instal·la-la. "Hola" apareix al menú i cada web té una pestanya "Hola DNS".

## El manifest

```json
{
  "schema": 1,
  "id": "laempresa.copies-s3",
  "name": { "en": "S3 backups", "es": "Copias en S3", "ca": "Còpies a S3" },
  "description": "Descripció curta (un text simple es pren com a anglès).",
  "version": "1.0.0",
  "publisher": { "name": "La Teva Empresa", "url": "https://example.com" },
  "homepage": "https://example.com/openplex",
  "openplex": ">=0.16.0",
  "permissions": ["domains:read"],
  "events": ["installed", "settings.changed", "domain.created"],
  "backend": {
    "image": "ghcr.io/laempresa/copies-s3:1.0.0",
    "archives": { "amd64": "images/amd64.tar", "arm64": "images/arm64.tar" },
    "port": 8080,
    "memory_mb": 128,
    "cpus": 0.5
  },
  "settings": [
    { "key": "api_token", "type": "secret", "label": "Token d'API", "required": true },
    { "key": "region", "type": "select", "label": "Regió", "options": [{ "value": "eu", "label": "Europa" }] }
  ],
  "pages": [{ "id": "main", "title": "Còpies a S3", "path": "/", "roles": ["admin"] }],
  "site_tabs": [{ "id": "site", "title": "S3", "path": "/site", "area": "backups" }]
}
```

| Camp | Significat |
|---|---|
| `id` | Únic, en minúscules, amb lletres/dígits/`.`/`-`. Posa-hi el teu nom davant: `laempresa.cosa`. No pot canviar entre versions. |
| `name`, `description`, títols, etiquetes | Un text (anglès) o `{"en": …, "es": …, "ca": …}`. El tauler mostra l'idioma de l'usuari i, si falta, l'anglès. |
| `version` | `MAJOR.MENOR.PEDAÇ`. Instal·lar una altra versió del mateix `id` l'actualitza i conserva la configuració i `/data`. |
| `openplex` | Versió mínima del tauler, `">=X.Y.Z"`. |
| `permissions` | El que l'extensió pot fer amb l'API (taula a sota). Es mostra a l'administrador abans d'instal·lar. |
| `events` | Esdeveniments que vol rebre (a sota). |
| `backend.image` | Referència de la imatge. Si `archives` té un fitxer per a l'arquitectura del servidor, OpenPlex el carrega (sortida de `docker save`, etiquetada exactament amb aquesta referència); si no, baixa la imatge del seu registre. |
| `backend.port` | Port on escolta el contenidor (1024–65535); també a `$PORT`. |
| `backend.memory_mb`, `cpus` | Límits; per defecte 128 MB i 0,5 CPU, com a màxim 1024 MB i 2 CPU. |
| `settings` | Camps del formulari de configuració: `text`, `secret` (no es torna a mostrar mai), `boolean` (`"true"`/`"false"`), `select` (amb `options`). `required`, `default` i `help` són opcionals. |
| `pages` | Pàgines al menú, sota *Extensions*. `roles`: `admin`, `reseller`, `customer` (per defecte només admin). |
| `site_tabs` | Pestanyes a la pàgina d'un web. Per defecte les veu tothom qui pot obrir el web; `area` (`site`, `files`, `dns`, `mail`, `backups`) s'exigeix als usuaris amb qui s'ha compartit el web. |

## Permisos

| Permís | Peticions a l'API permeses |
|---|---|
| `domains:read` | `GET /api/v1/domains`, `GET /api/v1/domains/{id}` |
| `dns:read` | `GET /api/v1/domains/{id}/dns` |
| `dns:write` | `POST /api/v1/domains/{id}/dns/records`, `PATCH` i `DELETE /api/v1/domains/{id}/dns/records/{rid}` |
| `mail:read` | `GET /api/v1/domains/{id}/mail` |
| `databases:read` | `GET /api/v1/databases`, `GET /api/v1/databases/{id}` |
| `users:read` | `GET /api/v1/users`, `GET /api/v1/users/{id}` |
| `system:read` | `GET /api/v1/system/info`, `GET /api/v1/system/stats` |
| `edge:write` | `GET` i `PUT /api/v1/extension/edge` (mira a sota) |
| `files:read` | `GET /api/v1/domains/{id}/files`, `…/files/content`, `…/files/download`; i els fitxers de tots els webs muntats en només lectura a `/sites/<domini>/` |
| `files:write` | `PUT /api/v1/domains/{id}/files/content`; `POST …/files/upload`, `…/mkdir`, `…/rename`, `…/delete`, `…/chmod` |
| `alerts:send` | `POST /api/v1/extension/alert` (mira a sota) |

Sempre permeses: `GET /api/v1/extension`, `GET /api/v1/auth/me`,
`GET /api/v1/system/health`. Tota la resta respon `403`. Necessites una
altra cosa? Obre un issue: els permisos nous s'afegeixen al nucli.

## Cridar l'API

Les peticions pel sòcol ja arriben autenticades com la teva extensió: no hi
ha cap token per desar. Les respostes són l'API JSON normal del tauler.

```bash
curl --unix-socket "$OPENPLEX_SOCKET" http://openplex/api/v1/domains
```

```go
client := &http.Client{Transport: &http.Transport{
    DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv("OPENPLEX_SOCKET"))
    }}}
res, err := client.Get("http://openplex/api/v1/domains")
```

```js
// Node.js
http.get({ socketPath: process.env.OPENPLEX_SOCKET, path: "/api/v1/domains" }, (res) => { /* … */ });
```

```python
import http.client, json, os, socket

class Panel(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.connect(os.environ["OPENPLEX_SOCKET"])

c = Panel("openplex")
c.request("GET", "/api/v1/domains")
domains = json.load(c.getresponse())
```

**Actuar com un usuari.** Per defecte l'extensió veu tot el que permeten els
seus permisos, de tots els comptes. Quan responguis a un usuari, afegeix la
capçalera `X-OpenPlex-User: <id>` (l'id que OpenPlex t'envia amb cada visita
a una pàgina): llavors l'API aplica també les regles d'accés d'aquest
usuari, així que un client només obté els seus propis webs.

**`GET /api/v1/extension`** retorna la teva configuració (secrets inclosos),
la clau de llicència, la versió del tauler i el teu **token**:

```json
{ "id": "example.hello", "version": "1.0.0", "settings": { "greeting": "Hello" },
  "license_key": "", "token": "…", "panel_version": "0.16.0" }
```

### Com reben visites els webs (`edge:write`)

Una extensió que es posa davant del servidor, com una CDN, pot demanar al
servidor web d'entrada que hi confiï i que alguns webs només acceptin
visites a través d'ella:

```json
PUT /api/v1/extension/edge
{ "trusted_proxies": ["173.245.48.0/20", "2400:cb00::/32"],
  "client_ip_headers": ["CF-Connecting-IP"],
  "restrict": [{ "host": "shop.example.com", "allow": ["173.245.48.0/20", "2400:cb00::/32"] }] }
```

- `trusted_proxies`: xarxes de les quals el servidor d'entrada creu les
  capçaleres de reenviament, perquè els webs vegin l'adreça real del
  visitant (`X-Forwarded-For`, `REMOTE_ADDR` de PHP). `client_ip_headers`
  indica les capçaleres que la porten, per ordre; necessita
  `trusted_proxies`.
- `restrict`: un host d'un web d'aquest servidor respon `403` a qualsevol
  altra xarxa (els reptes de certificat continuen passant). Restringeix un
  host només mentre totes les seves visites arribin per aquestes xarxes, o
  els visitants veuran l'error. Les connexions de les quals el servidor
  d'entrada no pot veure l'origen no es bloquegen mai: les peticions que el
  servidor es fa a si mateix i les visites IPv6 en un motor Docker que no pot
  donar IPv6 a la xarxa d'entrada (anterior a la 27), que Docker reenvia des
  de la passarel·la de la xarxa.
- Un `PUT` substitueix tota la teva política (`{}` l'esborra) i la retorna
  normalitzada; `GET` la retorna. Les xarxes no poden ser més àmplies que /8
  (IPv4) o /16 (IPv6), com a màxim 256 per llista. OpenPlex combina les
  polítiques de totes les extensions activades; desactivar o treure la teva
  l'elimina. Cada canvi queda al registre d'auditoria.

## Peticions d'OpenPlex

Cada petició que OpenPlex envia al teu contenidor porta
`X-OpenPlex-Token: <token>`. **Comprova-ho** (comparació en temps constant)
i rebutja tota la resta.

### Esdeveniments

`POST /openplex/events` amb:

```json
{ "type": "domain.created", "time": "2026-11-11T08:17:11Z",
  "data": { "domain_id": 8, "name": "botiga.example.com", "kind": "primary", "owner_id": 3 } }
```

| Esdeveniment | `data` |
|---|---|
| `installed` | — (després d'instal·lar o actualitzar) |
| `settings.changed` | — (llegeix els valors nous amb `GET /api/v1/extension`) |
| `domain.created`, `domain.deleted` | `domain_id`, `name`, `kind`, `owner_id` |
| `dns.changed` | `domain_id`, `zone`, `change` (`created`/`updated`/`deleted`), `record_id` |

Respon amb qualsevol codi 2xx. Davant d'un error o un 5xx, OpenPlex ho
reintenta unes quantes vegades. El lliurament no està garantit: si una cosa
no es pot perdre, comprova-la també amb l'API en arrencar.

### Pàgines i pestanyes de web

Quan un usuari obre una de les teves pàgines, el tauler la mostra en un marc
i OpenPlex passa la petició al teu `path`, afegint-hi:

| Capçalera | Valor |
|---|---|
| `X-OpenPlex-User`, `X-OpenPlex-Username`, `X-OpenPlex-Role` | Qui està mirant |
| `X-OpenPlex-Lang` | `en`, `es` o `ca` |
| `X-OpenPlex-Domain`, `X-OpenPlex-Read-Only` | Pestanyes de web: l'id del web i si l'usuari només pot mirar |
| `X-OpenPlex-Base` | Prefix de l'URL de les teves pàgines, p. ex. `/ext/example.hello/s/…/` |

L'URL de la pàgina també rep `?lang=…&theme=light|dark`. Regles per a les
pàgines:

- Fes servir **enllaços relatius** (`href="detall"`, `action="desar"`) o
  construeix-los amb `X-OpenPlex-Base`. Els formularis normals funcionen
  (respon un POST amb una redirecció `303`). La pàgina té un origen opac, així
  que un `fetch` a les teves pròpies rutes és d'un altre origen: respon-lo amb
  `Access-Control-Allow-Origin: *` (el que autoritza la petició és la sessió
  de l'URL).
- Les pàgines s'executen **aïllades** (sense mateix origen): sense galetes,
  sense `localStorage` i sense accés al tauler. Desa l'estat al teu servidor
  (`/data`), per usuari.
- Respecta `X-OpenPlex-Read-Only` a les pestanyes de web.
- Serveix el teu CSS i els teus scripts des del teu contenidor; segueix el
  `theme`.

## Avisos (`alerts:send`)

`POST /api/v1/extension/alert` amb `{"key", "subject", "body"}` envia un
correu als destinataris d'avisos del servidor (pàgina **Alertes**), en text
pla, amb una línia que diu quina extensió l'envia. S'envia una vegada per
`key` (tria'n una per troballa, p. ex. `"site-12:<sha256>"`), només si els
avisos estan activats i com a màxim 20 per hora. La resposta és
`{"sent": true|false}`.

## Emmagatzematge i límits

- `/data`: persistent i teu; es conserva entre reinicis i actualitzacions i
  s'esborra en treure l'extensió.
- `/tmp`: 64 MB, es buida en reiniciar. Tota la resta és de només lectura.
- `/sites`: amb `files:read`, la carpeta de cada web (`/sites/<domini>/`, amb
  `public/` i `logs/`), en només lectura. El contenidor rep també el grup
  root, només per poder entrar a aquesta carpeta; els fitxers que un web ha
  fet privats (mode `600`) continuen sense poder-se llegir. Els fitxers es
  canvien amb l'API (`files:write`).
- Entorn: `PORT`, `OPENPLEX_SOCKET`, `OPENPLEX_DATA`,
  `OPENPLEX_EXTENSION`, `OPENPLEX_EXTENSION_VERSION`.
- El contenidor s'executa amb un uid sense privilegis que tria OpenPlex (no
  depenguis de `USER` al teu Dockerfile) i es reinicia si s'atura.

## Empaquetar i signar

```bash
openplexd ext pack [-key FITXER] [-o FITXER] CARPETA
```

comprova el manifest i els fitxers que esmenta i escriu
`<id>-<version>.opx` (els fitxers que comencen per `.` s'ometen). Per a cada
arquitectura que suportis, construeix la imatge i desa-la amb `docker save`
a la ruta indicada a `archives`, o publica la imatge en un registre i no hi
posis `archives`.

**Signatura.** Crea una clau una vegada
(`openplexd ext keygen -id laempresa-2026 -o clau.json`) i empaqueta amb
`-key clau.json`. OpenPlex marca com a *verificats per OpenPlex* els paquets
signats amb la clau del projecte OpenPlex, i com a *revisats pel marketplace
d'OpenPlex* els que el marketplace ha revisat i signat (extensions de tercers,
OpenPlex ≥ 0.33.0); qualsevol altre paquet (sense
signar o signat amb la teva pròpia clau) també el pot pujar un
administrador, a qui s'avisa que OpenPlex no l'ha revisat. Les
actualitzacions han de mantenir el mateix `id`.

**Icona.** Posa un `icon.svg`, `icon.png` o `icon.webp` al costat
d'`openplex-extension.json` (quadrada, d'almenys 128×128 píxels i com a màxim
256 KB): va dins del paquet, signada amb la resta, i el marketplace la mostra
a les seves pàgines i al tauler.

## El marketplace

Les extensions, gratuïtes i de pagament, s'instal·len des
d'**Extensions → Marketplace**. Les de pagament demanen la clau de llicència
rebuda en comprar-les; OpenPlex la desa i la dona a l'extensió
(`license_key`), que la pot comprovar amb el seu proveïdor.

**Vendre la teva extensió.** Els desenvolupadors poden regalar o vendre les
seves pròpies extensions al marketplace: registra't a
[marketplace.openplex.dev/developers](https://marketplace.openplex.dev/developers),
reserva un prefix d'ID (les teves extensions són `<prefix>.<nom>`) i puja
cada versió com a `.opx`. Cada versió es revisa i després la signa el
marketplace, de manera que els panells la mostren com a *revisada pel
marketplace d'OpenPlex* (la instal·la OpenPlex 0.33.0 o posterior). Les de
pagament et paguen el 80% del preu sense IVA a través de Stripe, 14 dies
després de cada venda; el marketplace ven les llicències i s'ocupa de l'IVA i
de les claus (comprova-les amb
`POST https://marketplace.openplex.dev/api/v1/licenses/verify`).
