# Webs

[English](../en/websites.md) · [Español](../es/websites.md) · **Català**

Tot sobre allotjar un web: crear-lo, instal·lar-hi una aplicació, canviar-ne
la configuració de PHP i accedir als seus fitxers per SFTP.

## Crear un web

**Llocs web i dominis → Afegeix un lloc web.** Escriu el nom de host (apunta abans
el seu registre DNS A/AAAA al servidor, perquè es pugui emetre el certificat
HTTPS), tria el propietari i com funciona:

| Entorn | Per a |
|---|---|
| Estàtic | Fitxers HTML, CSS i JavaScript, sense codi de servidor |
| PHP 7.4 – 8.4 | WordPress, Joomla, Drupal, Laravel i qualsevol codi PHP |
| Node.js, Python, Go | La teva pròpia aplicació: indica l'ordre d'arrencada; escolta a `$PORT` |

Cada web funciona en el seu propi contenidor, amb el seu propi usuari del
sistema i els seus propis límits de memòria i CPU (només els administradors
els canvien). Els seus fitxers són a `/var/lib/openplex/sites/<nom>/`; el
servidor web serveix l'**arrel de documents**, `public/` per defecte.

Els subdominis (`blog.example.com`) són webs propis. Els **àlies** són noms
addicionals que mostren el mateix web (`www.example.com`).

## Aplicacions: WordPress, Joomla, Drupal

En crear un web, tria una **Aplicació**. També la pots instal·lar després en
un web buit, des d'**Aplicació → Instal·lar una aplicació** al seu resum.

1. Indica el títol del lloc, l'usuari i el correu de l'administrador, i la
   seva contrasenya (se'n genera una: copia-la, OpenPlex no la desa).
   WordPress i Drupal es poden instal·lar en anglès, castellà o català; Joomla
   en anglès (afegeix idiomes des de la seva administració).
2. OpenPlex passa el web a PHP, en una versió que l'aplicació admeti (Joomla 6
   i Drupal 11 necessiten PHP 8.3 o posterior), i li crea una base de dades
   MariaDB, que compta a la quota de bases de dades del propietari.
3. Es descarrega l'última versió des del mateix projecte i s'instal·la a
   l'arrel de documents. Triga un o dos minuts; la targeta **Aplicació** en
   mostra el progrés i després la versió, la base de dades i un enllaç a
   l'administració.

L'arrel de documents ha d'estar buida: l'instal·lador mai sobreescriu fitxers.
Si una instal·lació falla, esborra els fitxers que va escriure i la seva base
de dades, i la targeta n'explica el motiu; ho pots tornar a provar. Només el
propietari del web (i qui gestiona aquest compte) pot instal·lar aplicacions,
perquè la base de dades es crea al seu nom.

WordPress pot començar en anglès uns dies després d'una versió nova, fins que
se'n publiqui la traducció; després l'ofereix a **Escriptori →
Actualitzacions**.

### Actualitzar una aplicació

La targeta **Aplicació** mostra què es pot actualitzar: la mateixa aplicació
i, al WordPress, els seus connectors i temes; al Drupal, els seus mòduls.
OpenPlex ho comprova un cop al dia; **Comprova ara** ho comprova al moment.
**Actualitza** et deixa triar què vols actualitzar i després:

1. fa una còpia de seguretat dels fitxers del web i de la base de dades de
   l'aplicació (apareix a **Còpies de seguretat** marcada *abans d'una
   actualització*); si la còpia falla, no s'actualitza res;
2. actualitza amb les eines de la mateixa aplicació: WP-CLI al WordPress (amb
   l'actualització de la base de dades i les traduccions), la línia d'ordres
   del Joomla al Joomla, Composer i Drush al Drupal (el Drush s'afegeix al
   projecte el primer cop; mentrestant el web és en mode de manteniment, i si
   el Composer falla es torna a deixar el codi anterior);
3. comprova que el web encara respon. Si dona un error de servidor, la targeta
   indica que l'actualització ha fallat: restaura la còpia des de la pestanya
   **Còpies de seguretat** del web.

Notes:

- Les extensions del Joomla s'actualitzen des de la seva administració
  (**Sistema → Actualitza → Extensions**); la targeta les mostra.
- El Drupal s'actualitza dins de les versions que permet el seu
  `composer.json`: no s'ofereix una versió major nova ni un mòdul fixat a una
  versió exacta.
- Per actualitzar, el web ha d'estar en marxa i el servidor necessita accés a
  Internet; sol trigar menys d'un minut (uns minuts al Drupal).
- Només el propietari del web (i qui gestiona aquell compte) pot comprovar i
  actualitzar, igual que en instal·lar. També pots continuar actualitzant des
  de l'administració de l'aplicació.

### Comprovacions de seguretat

La part **Seguretat** de la targeta **Aplicació** mostra el que afebleix el
web; es comprova juntament amb les actualitzacions (cada dia o amb
**Comprova ara**):

| Aplicació | Comprovacions (✓ = OpenPlex ho pot corregir) |
|---|---|
| WordPress | editor de temes i connectors activat ✓, mode de depuració activat ✓, XML-RPC activat ✓, `wp-config.php` llegible per altres usuaris ✓, un administrador anomenat `admin`, connectors inactius |
| Joomla | mode de depuració activat ✓, errors de PHP visibles ✓, HTTPS no obligatori (si el web té HTTPS) ✓, carpeta `installation` oblidada ✓, `configuration.php` llegible per altres usuaris ✓, un usuari anomenat `admin` |
| Drupal | paquets amb vulnerabilitats conegudes (`composer audit`), sense noms d'amfitrió de confiança ✓, `update.php` obert a qualsevol ✓, errors de PHP visibles ✓, `settings.php` modificable ✓, l'administrador principal anomenat `admin` |
| Tots els webs | HTTPS no obligatori, una versió de PHP sense correccions de seguretat (7.4, 8.0, 8.1), errors de PHP mostrats a les pàgines |

Marca els problemes que vulguis corregir i prem **Corregeix els problemes
seleccionats**. Les correccions són les habituals: `DISALLOW_FILE_EDIT` i
`WP_DEBUG` a `wp-config.php`, un petit connector obligatori que respon a
XML-RPC amb un 403 (esborra
`wp-content/mu-plugins/openplex-disable-xmlrpc.php` per tornar-lo a activar),
la configuració global del Joomla, línies marcades `// OpenPlex security` al
final del `settings.php` del Drupal (els amfitrions de confiança són el nom
del web i els seus àlies), i permisos privats o només de lectura. Si després
el web deixa de respondre, el fitxer de configuració torna a quedar com
estava. La resta (un compte `admin`, les opcions del mateix web) els canvies
tu.

Les instal·lacions noves ja no proposen `admin` com a nom de
l'administrador.

### Còpies de staging

Una còpia de staging és una còpia a part d'un web, en un subdomini, per
provar actualitzacions, connectors o canvis abans que els vegin els
visitants.

**Crea una còpia de staging** (a la targeta **Aplicació**) crea el web
`staging.<el teu web>` (o un altre nom que triïs) amb el mateix entorn,
versió i configuració de PHP, una base de dades nova, i una còpia dels
fitxers i de la base de dades del web en producció. Després la còpia
s'adapta: la seva configuració apunta a la seva pròpia base de dades, es
reescriuen les adreces del WordPress, i el WordPress i el Joomla demanen als
cercadors que no la indexin. El web en producció no es modifica.

El nom de staging ha d'apuntar al servidor, com el web en producció (ja ho fa
si el tauler gestiona el teu DNS), perquè la còpia sigui accessible i
obtingui el seu certificat HTTPS. Compta com un web i una base de dades del
compte.

A la còpia, **Envia a `<web en producció>`** la torna a copiar sobre el web
en producció. Tria:

- **Fitxers**: codi, connectors, temes i fitxers pujats. Es conserva el
  fitxer de configuració de producció (`wp-config.php`, `configuration.php` o
  `sites/default/settings.php`), així que el web en producció continua fent
  servir la seva pròpia base de dades.
- **Base de dades**: contingut i opcions. El que hagi canviat al web en
  producció des que es va fer la còpia (comentaris, comandes, usuaris
  nous…) es perd.

Abans d'enviar, OpenPlex fa una còpia de seguretat del web en producció i de
la seva base de dades (marcada *abans d'enviar un staging* a **Còpies de
seguretat**); si falla, no es canvia res. Després, el WordPress recupera les
seves adreces i la seva opció per als cercadors, es reconstrueixen les
memòries cau del Drupal i OpenPlex comprova que el web en producció encara
respon; si no, restaura aquella còpia.

Per tenir una còpia nova, esborra el web de staging i crea una altra còpia.
La seva base de dades es queda fins que l'esborris a **Bases de dades**
(esborrar el web en producció també esborra els seus subdominis, incloses les
còpies de staging).

## Configuració de PHP

En un web PHP, **Configuració → Configuració de PHP** canvia aquests valors.
Els camps buits mantenen el valor per defecte (en gris).

| Ajust | Per defecte | Límits |
|---|---|---|
| Memòria per script (`memory_limit`) | 256M | de 32M a la memòria del web |
| Pujada de fitxer màxima (`upload_max_filesize`) | 64M | fins a 2048M |
| Enviament de formulari màxim (`post_max_size`) | 64M | fins a 2048M, com a mínim la mida de pujada |
| Temps màxim d'script (`max_execution_time`) | 60 s | 1 – 3600 |
| Temps per llegir una petició (`max_input_time`) | 60 s | 1 – 3600 |
| Camps de formulari per petició (`max_input_vars`) | 3000 | 100 – 100000 |
| Durada de la sessió (`session.gc_maxlifetime`) | 1440 s | 60 – 604800 |
| Mostrar els errors a les pàgines (`display_errors`) | No | Sí / No |
| Obrir URL remotes com a fitxers (`allow_url_fopen`) | Sí | Sí / No |
| Zona horària (`date.timezone`) | UTC | p. ex. `Europe/Madrid` |
| Funcions desactivades (`disable_functions`) | cap | noms separats per comes |

En desar es reinicia el contenidor del web (uns segons). Els valors s'apliquen
al web, a les seves tasques programades i al seu terminal web per igual.
Deixa **Mostrar els errors** desactivat en un web en producció: els missatges
d'error poden revelar camins i configuració als visitants.

## Fitxers per SFTP

A més del gestor de fitxers (pestanya **Fitxers**), cada usuari pot accedir
als fitxers dels seus webs amb un client SFTP com FileZilla, WinSCP o
Cyberduck. Les dades són a **Claus API i SSH → Accés SFTP**:

| | |
|---|---|
| Host | l'adreça del tauler (o del servidor) |
| Port | 2222 |
| Usuari | el teu usuari del tauler |
| Contrasenya | una **contrasenya SFTP** creada en aquesta pàgina, o una de les teves **claus SSH** |

La contrasenya SFTP és diferent de la del tauler i no pot ser la mateixa: els
clients SFTP la desen, i mai serveix per entrar al tauler. Per fer servir una
clau SSH, afegeix-ne la part pública a **Claus SSH**, a la mateixa pàgina.

En entrar veuràs una carpeta per cada web els fitxers del qual pots gestionar:
els teus webs, els dels comptes que gestiones i els webs compartits amb tu amb
l'àrea **Fitxers** (de només lectura si ho és l'accés compartit). Els fitxers
nous pertanyen a l'usuari del web, com amb el gestor de fitxers. No es poden
crear enllaços, moure fitxers entre webs ni executar ordres.

En la primera connexió el client mostra l'empremta de la clau del servidor:
compara-la amb la de la targeta **Accés SFTP**. Els intents d'inici de sessió
fallits repetits des d'una adreça la bloquegen (Fail2Ban), igual que al tauler.

El servidor escolta al port 2222 (`OPENPLEX_SFTP_LISTEN`, vegeu
[instal·lació](installation.md#configuració)); el tallafocs del tauler el manté
obert. En un servidor que fa servir ufw, l'obre l'instal·lador; si ufw encara el
bloqueja (per exemple, després d'actualitzar des del tauler), la targeta
**Accés SFTP** ho avisa, i un administrador executa `ufw allow 2222/tcp` al
servidor.
