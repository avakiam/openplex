# Referencia de la API

[English](../en/api.md) · **Español** · [Català](../ca/api.md)

Todo lo que hace el panel pasa por la misma API REST, así que un sistema de
facturación, un script o tu propio portal pueden hacerlo también. La lista
completa de endpoints, con lo que hace cada uno, y los campos que usa un
sistema de facturación están en la [versión inglesa](../en/api.md#provisioning-fields); una prueba del código falla si la
lista y el servidor no coinciden.

## Lo básico

- **URL base:** `https://<dominio del panel>/api/v1` (o la dirección directa
  del panel, `http://127.0.0.1:7080/api/v1` por defecto).
- **Autenticación:** crea una clave de API en la página **Claves API** (o con
  `POST /api-keys`) y envíala en cada petición:
  `Authorization: Bearer opx_…`. La clave actúa como la cuenta que la creó, con
  su rol y sus accesos: la clave de un administrador puede hacerlo todo; la de un
  cliente, solo lo que puede ese cliente. Las claves pueden caducar
  (`expires_in_days`) y se borran en la misma página.
- **Formato:** JSON de entrada y salida, campos en `snake_case`, fechas en
  RFC 3339 UTC, identificadores enteros.
- **Errores:** un código 4xx o 5xx con
  `{"error": "not_found", "message": "resource not found"}`. `error` es un
  código estable para decidir qué hacer; `message` es texto en inglés para
  personas. Los mensajes con valores llevan también `template` y `params`.
- **Roles:** las secciones marcadas *admin* responden `403` a las demás
  cuentas. En el resto, cada cuenta ve y cambia solo lo suyo o lo que se le ha
  compartido; lo demás responde `404`.
- **WebSockets** (los endpoints `…/ws`): los navegadores no pueden enviar
  cabeceras ahí, así que primero pide un ticket de un solo uso con
  `POST /auth/ws-ticket` y conecta a `wss://…/ws?ticket=…`.
- **Sin clave de API:** `POST /auth/login` devuelve un token de acceso de corta
  duración (se envía como `Bearer`, igual que una clave) y fija una cookie para
  renovarlo con `POST /auth/refresh`. Es lo que usa el panel; las integraciones
  deberían usar claves de API.

Ejemplo: crear una web y consultar su estado.

```bash
curl -s -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"name": "example.com", "runtime": "php", "runtime_version": "8.3"}' \
  https://panel.example.com/api/v1/domains
curl -s -H "Authorization: Bearer $KEY" https://panel.example.com/api/v1/domains/12
```

Los cuerpos de las peticiones y respuestas siguen lo que muestra el panel en
cada página; la forma más fácil de ver uno es la pestaña de red del navegador
mientras haces lo mismo en el panel.
