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

## Emmagatzematge i límits

- `/data`: persistent i teu; es conserva entre reinicis i actualitzacions i
  s'esborra en treure l'extensió.
- `/tmp`: 64 MB, es buida en reiniciar. Tota la resta és de només lectura.
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
signats amb la clau del projecte OpenPlex; qualsevol altre paquet (sense
signar o signat amb la teva pròpia clau) també el pot pujar un
administrador, a qui s'avisa que OpenPlex no l'ha revisat. Les
actualitzacions han de mantenir el mateix `id`.

## El marketplace

Les extensions oficials, gratuïtes i de pagament, s'instal·len des
d'**Extensions → Marketplace**. Les de pagament demanen la clau de llicència
rebuda en comprar-les; OpenPlex la desa i la dona a l'extensió
(`license_key`), que la pot comprovar amb el seu proveïdor. Per publicar una
extensió al marketplace d'OpenPlex, contacta amb nosaltres amb un issue al
[repositori principal](https://github.com/avakiam/openplex).
