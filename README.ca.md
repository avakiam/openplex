<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img src="docs/assets/logo-light.svg" alt="OpenPlex" height="56">
  </picture>
</h1>

[English](README.md) · [Español](README.es.md) · **Català**

OpenPlex és un tauler de control d'allotjament web de codi obert, una
alternativa a Plesk i cPanel basada en contenidors. S'instal·la amb una sola
ordre en un servidor Ubuntu o Debian net i us ofereix, a tu, al teu equip i
als teus clients, un tauler modern per a webs, bases de dades, correu, DNS,
còpies de seguretat i seguretat.

> **Estat: versió prèvia.** OpenPlex està en ple desenvolupament i encara no
> ha tingut la seva primera versió pública. Hi pot haver canvis incompatibles
> fins a la 1.0.

## Per què OpenPlex

- **Un sol binari.** `openplexd` és un únic programa en Go amb el tauler
  inclòs i el seu estat en una base de dades SQLite integrada. Al servidor
  només cal Docker.
- **Cada web al seu propi contenidor**, amb el seu propi usuari, en una xarxa
  privada per compte. El web d'un client no pot llegir, arribar ni alentir el
  d'un altre.
- **HTTPS automàtic** per a cada web, àlies i el mateix tauler, mitjançant un
  proxy Caddy (Let's Encrypt / ZeroSSL).
- **Programari lliure** (AGPL-3.0) amb un model d'extensions obert:
  qualsevol pot crear extensions, gratuïtes o de pagament.

## Funcions

| | |
|---|---|
| **Webs** | Estàtics, PHP 7.4–8.4 (amb les extensions que necessiten WordPress, Laravel o Drupal), Node.js, Python i Go; configuració de PHP per web (memòria, mida de pujada, temps, zona horària…); WordPress, Joomla i Drupal amb un clic (última versió, base de dades pròpia, en anglès, castellà o català) i les seves actualitzacions, amb còpia de seguretat prèvia; àlies i subdominis; redireccions; variables d'entorn; engegar/aturar/reiniciar; registres d'accés i d'errors; límits de recursos |
| **Fitxers** | Gestor de fitxers web amb editor, pujada, comprimir/descomprimir i permisos; SFTP (port 2222, claus SSH o una contrasenya només per a SFTP); terminal web al contenidor del web |
| **Bases de dades** | MariaDB, PostgreSQL i Redis; Adminer amb un clic; rotació de contrasenyes |
| **Correu** | Bústies, àlies i reenviaments, quotes, registres DKIM/SPF/DMARC, webmail Roundcube amb accés en un clic |
| **DNS** | DNS autoritatiu (PowerDNS) amb zones creades per als teus dominis |
| **Còpies de seguretat** | Còpies xifrades i deduplicades (restic) en un disc local o a S3, programades; restauració d'un web, una base de dades, un domini de correu o una sola bústia; reconstrucció completa del servidor amb `openplexd restore-panel` |
| **Seguretat** | Tallafocs del servidor (nftables) amb aplicació segura i marxa enrere, Fail2Ban, captcha a l'inici de sessió (Turnstile, hCaptcha, reCAPTCHA), verificació en dos passos, registre d'auditoria |
| **Usuaris** | Administradors, revenedors i clients amb quotes; compartir un web (només algunes àrees, opcionalment en només lectura) o una base de dades amb un altre usuari; claus d'API |
| **Tasques i monitoratge** | Tasques programades per web; CPU, memòria, disc i xarxa del servidor i dels contenidors; alertes per correu (còpia fallida, web caigut, disc gairebé ple, certificat sense renovar, actualitzacions) |
| **Idiomes** | Anglès, castellà i català |

## Requisits

- Ubuntu 22.04 o superior, o Debian 12 o superior, en amd64 o arm64.
- Accés root, una IP pública i els ports 80 i 443 lliures.
- Es recomanen 2 GB de RAM o més (el correu i les bases de dades són el que més fa servir).
- Un domini que apunti al servidor, per al tauler i per a HTTPS.

## Instal·lació

Quan es publiqui la primera versió:

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | sudo bash -s -- \
  --panel-domain panel.example.com --acme-email tu@example.com
```

L'instal·lador configura Docker, el servei `openplexd` i el tallafocs, i
mostra l'adreça del tauler. Entra com a `admin` amb la contrasenya desada a
`/var/lib/openplex/initial-admin-password` (el fitxer s'esborra quan canvies
aquesta contrasenya).

A la [guia d'instal·lació](docs/ca/installation.md) hi ha totes les opcions,
les actualitzacions i com reconstruir un servidor perdut.

## Documentació

- [Instal·lació, actualitzacions i recuperació](docs/ca/installation.md)
- [Webs: aplicacions, configuració de PHP i SFTP](docs/ca/websites.md)
- [OpenPlex per a proveïdors d'allotjament](docs/ca/hosting-providers.md)
- [Llicència, extensions i marca](docs/ca/licensing.md)
- [Crear una extensió](docs/ca/extensions.md)

## Compilar des del codi

Necessites Go 1.26 o superior i Node.js 22 o superior.

```bash
make build   # tauler (exportació estàtica de Next.js) + bin/openplexd
make check   # go vet, proves i compilació sense CGO
```

Sense Go instal·lat? Fes servir la imatge oficial:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26-bookworm make check
```

## Llicència

Copyright © 2026 Avakiam.

El nucli d'OpenPlex es distribueix sota la
[GNU Affero General Public License v3.0 (només aquesta versió)](LICENSE). Si
modifiques OpenPlex i l'ofereixes a altres, també a través de la xarxa, has
de publicar els teus canvis sota la mateixa llicència.

Les extensions que només fan servir la interfície d'extensions documentada
poden tenir qualsevol llicència, també privativa i de pagament: mira
l'[excepció per a extensions d'OpenPlex](LICENSE-EXCEPTION.md) (resum en
català a [docs/ca/licensing.md](docs/ca/licensing.md)). OpenPlex oferirà
extensions oficials, unes de gratuïtes i de codi obert i d'altres de
pagament.

"OpenPlex" i els seus logotips són marques d'Avakiam: mira
[TRADEMARKS.md](TRADEMARKS.md).

## Seguretat

Has trobat una vulnerabilitat? Avisa'ns en privat; mira [SECURITY.md](SECURITY.md).

## Contribuir

Els avisos d'errors, idees i correccions de traducció són benvinguts com a
issues. Encara no acceptem contribucions de codi; mira
[CONTRIBUTING.md](CONTRIBUTING.md).
