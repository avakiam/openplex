# Referència de l'API

[English](../en/api.md) · [Español](../es/api.md) · **Català**

Tot el que fa el tauler passa per la mateixa API REST, de manera que un
sistema de facturació, un script o el teu propi portal també ho poden fer. La
llista completa d'endpoints, amb el que fa cadascun, i els camps que fa servir
un sistema de facturació són a la [versió anglesa](../en/api.md#provisioning-fields); una prova del codi falla si la llista
i el servidor no coincideixen.

## El bàsic

- **URL base:** `https://<domini del tauler>/api/v1` (o l'adreça directa del
  tauler, `http://127.0.0.1:7080/api/v1` per defecte).
- **Autenticació:** crea una clau d'API a la pàgina **Claus API** (o amb
  `POST /api-keys`) i envia-la a cada petició:
  `Authorization: Bearer opx_…`. La clau actua com el compte que la va crear,
  amb el seu rol i els seus accessos: la clau d'un administrador ho pot fer tot;
  la d'un client, només el que pot fer aquell client. Les claus poden caducar
  (`expires_in_days`) i s'esborren a la mateixa pàgina.
- **Format:** JSON d'entrada i sortida, camps en `snake_case`, dates en
  RFC 3339 UTC, identificadors enters.
- **Errors:** un codi 4xx o 5xx amb
  `{"error": "not_found", "message": "resource not found"}`. `error` és un codi
  estable per decidir què fer; `message` és text en anglès per a persones. Els
  missatges amb valors porten també `template` i `params`.
- **Rols:** les seccions marcades *admin* responen `403` als altres comptes. A
  la resta, cada compte veu i canvia només el que és seu o el que se li ha
  compartit; la resta respon `404`.
- **WebSockets** (els endpoints `…/ws`): els navegadors no hi poden enviar
  capçaleres, així que primer demana un tiquet d'un sol ús amb
  `POST /auth/ws-ticket` i connecta a `wss://…/ws?ticket=…`.
- **Sense clau d'API:** `POST /auth/login` retorna un token d'accés de curta
  durada (s'envia com a `Bearer`, igual que una clau) i fixa una galeta per
  renovar-lo amb `POST /auth/refresh`. És el que fa servir el tauler; les
  integracions haurien de fer servir claus d'API.

Exemple: crear un web i consultar-ne l'estat.

```bash
curl -s -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"name": "example.com", "runtime": "php", "runtime_version": "8.3"}' \
  https://panel.example.com/api/v1/domains
curl -s -H "Authorization: Bearer $KEY" https://panel.example.com/api/v1/domains/12
```

Els cossos de les peticions i respostes segueixen el que mostra el tauler a
cada pàgina; la manera més fàcil de veure'n un és la pestanya de xarxa del
navegador mentre fas el mateix al tauler.
