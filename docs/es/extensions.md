# Crear una extensión

[English](../en/extensions.md) · **Español** · [Català](../ca/extensions.md)

Una extensión de OpenPlex añade funciones al panel: páginas propias en el
menú, pestañas en la página de una web, ajustes y reacciones a lo que pasa en
el servidor (se crea una web, cambia un registro DNS…). Cualquiera puede
crear una, en cualquier lenguaje, y distribuirla gratis o de pago, abierta o
cerrada: mira [licencia](licensing.md).

Hay un ejemplo completo para copiar en
[`examples/extension-hello`](../../examples/extension-hello) (licencia 0BSD:
úsalo como quieras).

## Cómo funciona

- Una extensión es un **contenedor** más un **manifiesto**
  (`openplex-extension.json`), distribuidos en un paquete (`.opx`).
- OpenPlex ejecuta el contenedor con su propio usuario, sin privilegios
  (capabilities), con el sistema de archivos raíz en solo lectura y en su
  propia red (con Internet, sin nada más del servidor). Conserva `/data`
  entre reinicios y actualizaciones.
- La extensión escucha HTTP en `$PORT`. OpenPlex le envía **eventos** y le
  pasa las visitas a sus **páginas** de los usuarios que han iniciado sesión.
- La extensión llama a la **API de OpenPlex** por un socket unix,
  `$OPENPLEX_SOCKET` (`/run/openplex/panel.sock`), limitada a los
  **permisos** que aceptó el administrador al instalarla.

```
 navegador ──▶ OpenPlex ──(páginas, eventos: HTTP :$PORT)──▶ contenedor de la extensión
                  ▲                                                  │
                  └──────── API por /run/openplex/panel.sock ────────┘
```

## Empezar rápido

```bash
cd examples/extension-hello
docker build -t openplex-examples/hello:1.0.0 .
mkdir -p images && docker save -o images/amd64.tar openplex-examples/hello:1.0.0
openplexd ext pack .            # → example.hello-1.0.0.opx
```

Después, en el panel: **Extensiones → Subir extensión**, revísala e
instálala. "Hola" aparece en el menú y cada web tiene una pestaña "Hola DNS".

## El manifiesto

```json
{
  "schema": 1,
  "id": "tuempresa.copias-s3",
  "name": { "en": "S3 backups", "es": "Copias en S3", "ca": "Còpies a S3" },
  "description": "Descripción corta (un texto simple se toma como inglés).",
  "version": "1.0.0",
  "publisher": { "name": "Tu Empresa", "url": "https://example.com" },
  "homepage": "https://example.com/openplex",
  "openplex": ">=0.16.0",
  "permissions": ["domains:read"],
  "events": ["installed", "settings.changed", "domain.created"],
  "backend": {
    "image": "ghcr.io/tuempresa/copias-s3:1.0.0",
    "archives": { "amd64": "images/amd64.tar", "arm64": "images/arm64.tar" },
    "port": 8080,
    "memory_mb": 128,
    "cpus": 0.5
  },
  "settings": [
    { "key": "api_token", "type": "secret", "label": "Token de API", "required": true },
    { "key": "region", "type": "select", "label": "Región", "options": [{ "value": "eu", "label": "Europa" }] }
  ],
  "pages": [{ "id": "main", "title": "Copias en S3", "path": "/", "roles": ["admin"] }],
  "site_tabs": [{ "id": "site", "title": "S3", "path": "/site", "area": "backups" }]
}
```

| Campo | Significado |
|---|---|
| `id` | Único, en minúsculas, con letras/dígitos/`.`/`-`. Ponle tu nombre delante: `tuempresa.cosa`. No puede cambiar entre versiones. |
| `name`, `description`, títulos, etiquetas | Un texto (inglés) o `{"en": …, "es": …, "ca": …}`. El panel muestra el idioma del usuario y, si falta, el inglés. |
| `version` | `MAYOR.MENOR.PARCHE`. Instalar otra versión del mismo `id` la actualiza y conserva ajustes y `/data`. |
| `openplex` | Versión mínima del panel, `">=X.Y.Z"`. |
| `permissions` | Lo que la extensión puede hacer con la API (tabla abajo). Se le muestra al administrador antes de instalar. |
| `events` | Eventos que quiere recibir (abajo). |
| `backend.image` | Referencia de la imagen. Si `archives` tiene un archivo para la arquitectura del servidor, OpenPlex lo carga (salida de `docker save`, etiquetada exactamente con esta referencia); si no, descarga la imagen de su registro. |
| `backend.port` | Puerto en el que escucha el contenedor (1024–65535); también en `$PORT`. |
| `backend.memory_mb`, `cpus` | Límites; por defecto 128 MB y 0,5 CPU, como máximo 1024 MB y 2 CPU. |
| `settings` | Campos del formulario de ajustes: `text`, `secret` (nunca se vuelve a mostrar), `boolean` (`"true"`/`"false"`), `select` (con `options`). `required`, `default` y `help` son opcionales. |
| `pages` | Páginas en el menú, bajo *Extensiones*. `roles`: `admin`, `reseller`, `customer` (por defecto solo admin). |
| `site_tabs` | Pestañas en la página de una web. Por defecto las ve todo el que puede abrir la web; `area` (`site`, `files`, `dns`, `mail`, `backups`) se exige a los usuarios con los que se compartió la web. |

## Permisos

| Permiso | Peticiones a la API permitidas |
|---|---|
| `domains:read` | `GET /api/v1/domains`, `GET /api/v1/domains/{id}` |
| `dns:read` | `GET /api/v1/domains/{id}/dns` |
| `dns:write` | `POST /api/v1/domains/{id}/dns/records`, `PATCH` y `DELETE /api/v1/domains/{id}/dns/records/{rid}` |
| `mail:read` | `GET /api/v1/domains/{id}/mail` |
| `databases:read` | `GET /api/v1/databases`, `GET /api/v1/databases/{id}` |
| `users:read` | `GET /api/v1/users`, `GET /api/v1/users/{id}` |
| `system:read` | `GET /api/v1/system/info`, `GET /api/v1/system/stats` |

Siempre permitidas: `GET /api/v1/extension`, `GET /api/v1/auth/me`,
`GET /api/v1/system/health`. Todo lo demás responde `403`. ¿Necesitas otra
cosa? Abre un issue: los permisos nuevos se añaden al núcleo.

## Llamar a la API

Las peticiones por el socket ya llegan autenticadas como tu extensión: no
hay ningún token que guardar. Las respuestas son la API JSON normal del
panel.

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

**Actuar como un usuario.** Por defecto la extensión ve todo lo que permiten
sus permisos, de todas las cuentas. Cuando respondas a un usuario, añade la
cabecera `X-OpenPlex-User: <id>` (el id que OpenPlex te envía con cada
visita a una página): la API aplica entonces también las reglas de acceso de
ese usuario, así que un cliente solo obtiene sus propias webs.

**`GET /api/v1/extension`** devuelve tus ajustes (secretos incluidos), la
clave de licencia, la versión del panel y tu **token**:

```json
{ "id": "example.hello", "version": "1.0.0", "settings": { "greeting": "Hello" },
  "license_key": "", "token": "…", "panel_version": "0.16.0" }
```

## Peticiones de OpenPlex

Cada petición que OpenPlex envía a tu contenedor lleva
`X-OpenPlex-Token: <token>`. **Compruébalo** (comparación en tiempo
constante) y rechaza todo lo demás.

### Eventos

`POST /openplex/events` con:

```json
{ "type": "domain.created", "time": "2026-11-11T08:17:11Z",
  "data": { "domain_id": 8, "name": "tienda.example.com", "kind": "primary", "owner_id": 3 } }
```

| Evento | `data` |
|---|---|
| `installed` | — (después de instalar o actualizar) |
| `settings.changed` | — (lee los valores nuevos con `GET /api/v1/extension`) |
| `domain.created`, `domain.deleted` | `domain_id`, `name`, `kind`, `owner_id` |
| `dns.changed` | `domain_id`, `zone`, `change` (`created`/`updated`/`deleted`), `record_id` |

Responde con cualquier código 2xx. Ante un error o un 5xx, OpenPlex lo
reintenta unas cuantas veces. La entrega no está garantizada: si algo no se
puede perder, compruébalo también con la API al arrancar.

### Páginas y pestañas de web

Cuando un usuario abre una de tus páginas, el panel la muestra en un marco y
OpenPlex pasa la petición a tu `path`, añadiendo:

| Cabecera | Valor |
|---|---|
| `X-OpenPlex-User`, `X-OpenPlex-Username`, `X-OpenPlex-Role` | Quién está mirando |
| `X-OpenPlex-Lang` | `en`, `es` o `ca` |
| `X-OpenPlex-Domain`, `X-OpenPlex-Read-Only` | Pestañas de web: el id de la web y si el usuario solo puede mirar |
| `X-OpenPlex-Base` | Prefijo de la URL de tus páginas, p. ej. `/ext/example.hello/s/…/` |

La URL de la página también recibe `?lang=…&theme=light|dark`. Reglas para
las páginas:

- Usa **enlaces relativos** (`href="detalle"`, `action="guardar"`) o
  constrúyelos con `X-OpenPlex-Base`. Los formularios normales y los `fetch`
  a tus propias rutas funcionan.
- Las páginas se ejecutan **aisladas** (sin mismo origen): sin cookies, sin
  `localStorage` y sin acceso al panel. Guarda el estado en tu servidor
  (`/data`), por usuario.
- Respeta `X-OpenPlex-Read-Only` en las pestañas de web.
- Sirve tu CSS y tus scripts desde tu contenedor; sigue el `theme`.

## Almacenamiento y límites

- `/data`: persistente y tuyo; se conserva entre reinicios y
  actualizaciones y se borra al quitar la extensión.
- `/tmp`: 64 MB, se vacía al reiniciar. Todo lo demás es de solo lectura.
- Entorno: `PORT`, `OPENPLEX_SOCKET`, `OPENPLEX_DATA`,
  `OPENPLEX_EXTENSION`, `OPENPLEX_EXTENSION_VERSION`.
- El contenedor se ejecuta con un uid sin privilegios que elige OpenPlex (no
  dependas de `USER` en tu Dockerfile) y se reinicia si se detiene.

## Empaquetar y firmar

```bash
openplexd ext pack [-key ARCHIVO] [-o ARCHIVO] CARPETA
```

comprueba el manifiesto y los archivos que menciona y escribe
`<id>-<version>.opx` (los archivos que empiezan por `.` se omiten). Para cada
arquitectura que soportes, construye la imagen y guárdala con `docker save`
en la ruta indicada en `archives`, o publica la imagen en un registro y no
pongas `archives`.

**Firma.** Crea una clave una vez
(`openplexd ext keygen -id tuempresa-2026 -o clave.json`) y empaqueta con
`-key clave.json`. OpenPlex marca como *verificados por OpenPlex* los
paquetes firmados con la clave del proyecto OpenPlex; cualquier otro paquete
(sin firmar o firmado con tu propia clave) también lo puede subir un
administrador, al que se avisa de que OpenPlex no lo ha revisado. Las
actualizaciones deben mantener el mismo `id`.

## El marketplace

Las extensiones oficiales, gratuitas y de pago, se instalan desde
**Extensiones → Marketplace**. Las de pago piden la clave de licencia
recibida al comprarlas; OpenPlex la guarda y se la da a la extensión
(`license_key`), que puede comprobarla con su proveedor. Para publicar una
extensión en el marketplace de OpenPlex, contacta con nosotros con un issue
en el [repositorio principal](https://github.com/avakiam/openplex).
