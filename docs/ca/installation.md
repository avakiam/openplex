# Instal·lació, actualitzacions i recuperació

[English](../en/installation.md) · [Español](../es/installation.md) · **Català**

## Abans de començar

- Un servidor net amb **Ubuntu 22.04+** o **Debian 12+** (amd64 o arm64),
  accés root i una IP pública. Cap altre servei ha de fer servir els ports 80
  i 443.
- Es recomanen 2 GB de RAM o més; amb 1 GB funcionen uns quants webs petits
  sense correu.
- Un nom per al tauler (per exemple `panel.example.com`) amb un registre A (i
  AAAA si tens IPv6) que apunti al servidor. Let's Encrypt el necessita per
  emetre el certificat del tauler.
- Si allotjaràs correu: un proveïdor que permeti trànsit sortint pel port 25,
  i el DNS invers (PTR) de la IP del servidor apuntant al nom del servidor de
  correu.

## Instal·lar

```bash
curl -fsSL https://github.com/avakiam/openplex/releases/latest/download/install.sh | sudo bash -s -- \
  --panel-domain panel.example.com --acme-email tu@example.com
```

L'instal·lador no fa preguntes i es pot tornar a executar sense risc:

1. instal·la Docker Engine des del repositori oficial de Docker (si no hi és),
   nftables i Fail2Ban;
2. baixa `openplexd`, en comprova la suma SHA-256 i l'instal·la a
   `/usr/local/bin`;
3. escriu `/etc/openplex/openplex.env` i el servei systemd `openplexd`;
4. engega el servei, que crea el compte d'administrador, el proxy i les
   regles del tallafocs.

### Opcions

| Opció | Què fa |
|---|---|
| `--panel-domain HOST` | Serveix el tauler a `https://HOST` a través del proxy |
| `--acme-email EMAIL` | Adreça de contacte per a Let's Encrypt / ZeroSSL |
| `--version VERSIÓ` | Instal·la aquesta versió en lloc de l'última |
| `--binary RUTA` | Instal·la un binari `openplexd` local en lloc de baixar-lo |
| `--listen ADR` | Adreça directa del tauler (per defecte `127.0.0.1:7080`, només accessible des del servidor) |
| `--no-firewall` | No toca el tallafocs del servidor (sense regles nftables ni Fail2Ban) |

## Primer accés

Obre `https://panel.example.com` i entra com a **`admin`**. La contrasenya és
a `/var/lib/openplex/initial-admin-password`:

```bash
sudo cat /var/lib/openplex/initial-admin-password
```

Canvia-la des del teu perfil i activa la verificació en dos passos. El fitxer
s'esborra sol quan canvies aquesta contrasenya. Si mai perds la contrasenya
de l'administrador:

```bash
sudo openplexd reset-password admin
```

## Ports

| Port | Ús |
|---|---|
| 22 | SSH (el tallafocs sempre el permet) |
| 80, 443 (TCP), 443 (UDP) | Webs, tauler i webmail (proxy) |
| 25, 465, 587, 143, 993, 110, 995, 4190 | Correu, si està activat |
| 53 (TCP/UDP) | DNS, si el servidor DNS està activat |
| 2222 | SFTP per als fitxers dels webs (el tallafocs sempre el permet; l'instal·lador també l'obre a `ufw`) |
| 3306, 5432 | Accés remot a bases de dades, només mentre alguna el té activat i només per a les adreces de la seva llista |

El tallafocs es gestiona a **Tallafocs** dins del tauler. Es queda apagat si
`ufw` està actiu, perquè no es trepitgin les regles.

## Configuració

Els paràmetres són a `/etc/openplex/openplex.env` (`CLAU=valor`; reinicia el
servei després de canviar-los). Els més útils:

| Variable | Per defecte | Què fa |
|---|---|---|
| `OPENPLEX_PANEL_DOMAIN` | — | Nom del tauler |
| `OPENPLEX_ACME_EMAIL` | — | Adreça de contacte dels certificats |
| `OPENPLEX_DATA_DIR` | `/var/lib/openplex` | Tot l'estat: base de dades, webs, claus, certificats |
| `OPENPLEX_LISTEN` | `127.0.0.1:7080` | Adreça directa del tauler |
| `OPENPLEX_FIREWALL` | `true` | Gestionar el tallafocs del servidor |
| `OPENPLEX_CAPTCHA` | `true` | `false` desactiva el captcha de l'inici de sessió (per exemple després de posar una clau errònia) |
| `OPENPLEX_TENANT_NETWORKS` | `10.213.0.0/16` | Rang d'adreces per a la xarxa privada de cada compte (una /24 cadascuna) |
| `OPENPLEX_SFTP_LISTEN` | `:2222` | Servidor SFTP per als fitxers dels webs (buit el desactiva) |
| `OPENPLEX_DB_REMOTE_MARIADB` | `:3306` | Accés remot a les bases de dades MariaDB (buit el desactiva) |
| `OPENPLEX_DB_REMOTE_POSTGRES` | `:5432` | Accés remot a les bases de dades PostgreSQL (buit el desactiva) |

## Alertes per correu

**Alertes** (administradors) envia un correu a les adreces que triïs quan
alguna cosa necessita atenció. Ho comprova cada minut:

| Alerta | Quan |
|---|---|
| Falla una còpia de seguretat | cada còpia fallida, un cop |
| Un web està caigut | després de dos minuts amb error, i un altre cop quan es recupera |
| El disc és gairebé ple | per sobre del 90 % del disc de dades, i un altre cop per sota del 85 % |
| Una base de dades supera el seu límit de mida | quan passa a només lectura, i un altre cop quan torna a estar-ne per sota (vegeu [bases de dades](databases.md#límits-de-mida)) |
| Un certificat no es renova | quan el d'un nom en ús és a punt de caducar (la renovació fa temps que falla: sol ser el DNS o el port 80) |
| Nova versió d'OpenPlex | un cop per versió |
| Falla una actualització d'OpenPlex | quan una actualització es desfà o falla |

Un problema en curs s'envia un cop, es recorda cada dia mentre dura i s'avisa
quan s'acaba; reiniciar el tauler no reenvia res. Els correus són en anglès,
castellà o català.

S'envien mitjançant el servei de correu d'aquest servidor (si **Correu** està
activat) o mitjançant un servidor SMTP, com el del teu proveïdor (STARTTLS, TLS
o cap; la contrasenya es desa xifrada). Amb el correu local, fes servir un
remitent d'un domini de correu allotjat aquí el registre SPF del qual autoritzi
el servidor, o els missatges poden acabar al correu brossa. **Enviar un correu
de prova** comprova la configuració abans de desar-la; la pàgina també mostra
les alertes dels darrers 90 dies i per què no se'n va poder enviar alguna.

## Gestionar el servei

```bash
systemctl status openplexd
journalctl -u openplexd -f
sudo systemctl restart openplexd
```

## Actualitzar

**Des del tauler** (OpenPlex 0.18 o posterior): **Actualitzacions** mostra la
versió instal·lada i, dues vegades al dia, cerca una nova versió estable a
GitHub; l'entrada del menú mostra un punt quan n'hi ha. **Actualitzar a …**
llavors:

1. descarrega la versió i la comprova: la suma de control i la signatura feta
   amb la clau de versions d'Avakiam (una versió signada d'una altra manera es
   rebutja);
2. atura el tauler aproximadament un minut (els webs, les bases de dades i el
   correu continuen funcionant), desa una còpia del binari actual i de la seva
   base de dades a `/var/lib/openplex/updates/`, instal·la la nova versió i
   l'engega;
3. si la nova versió no arrenca correctament en tres minuts, torna a posar
   sola el binari i la base de dades anteriors.

El resultat, i qui la va iniciar, queda al registre d'auditoria. Desactiva
**Cercar actualitzacions automàticament** si gestiones tu les versions (la
comprovació només es farà en prémer **Comprovar ara**).

**Amb l'instal·lador**: torna a executar-lo amb les mateixes opcions.
Substitueix el binari (comprovant també la signatura) i conserva totes les
dades; en engegar, el servei migra la seva base de dades i actualitza els
contenidors que gestiona. Fes una còpia abans.

## Còpies de seguretat i reconstruir un servidor perdut

Configura les còpies a **Còpies de seguretat**: una carpeta local o un bucket
S3, una programació i quantes còpies cal conservar. **Desa la clau de
recuperació que hi apareix en un lloc segur i fora del servidor**: sense ella
no es poden llegir les còpies.

Per reconstruir un servidor que ja no existeix:

1. Instal·la OpenPlex al servidor nou amb les mateixes opcions.
2. Restaura el tauler des del repositori de còpies:

   ```bash
   sudo systemctl stop openplexd
   sudo OPENPLEX_RECOVERY_KEY='…' openplexd restore-panel --repo s3:https://s3.example.com/bucket/openplex
   sudo systemctl start openplexd
   ```

   Amb S3, defineix també `AWS_ACCESS_KEY_ID` i `AWS_SECRET_ACCESS_KEY`. Si el
   repositori és local o està muntat, passa'n la ruta a `--repo`.

3. En engegar, OpenPlex restaura els webs, les bases de dades, el correu i el
   webmail de la mateixa còpia. Apunta el teu DNS a la IP nova si ha canviat
   (les zones allotjades a OpenPlex s'actualitzen soles).

`openplexd restore-panel --help` mostra totes les opcions (`--snapshot`,
`--ipv4`, `--ipv6`, `--force`…).

## Desinstal·lar

```bash
sudo systemctl disable --now openplexd
sudo docker rm -f $(sudo docker ps -aq --filter label=openplex.managed=true)
sudo docker volume rm openplex-mail-data openplex-webmail-db
sudo docker network prune -f --filter label=openplex.managed=true
sudo rm -rf /var/lib/openplex /etc/openplex /usr/local/bin/openplexd /etc/systemd/system/openplexd.service
```

Això esborra tots els webs, bases de dades i bústies del servidor.
