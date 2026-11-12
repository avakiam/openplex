# Websites

**English** · [Español](../es/websites.md) · [Català](../ca/websites.md)

Everything about hosting a website: creating it, installing an application,
changing its PHP settings and reaching its files over SFTP.

## Creating a website

**Websites & Domains → Add website.** Enter the host name (point its DNS A/AAAA
record at the server first, so the HTTPS certificate can be issued), choose
who owns it and how it runs:

| Runtime | For |
|---|---|
| Static | HTML, CSS and JavaScript files, no server code |
| PHP 7.4 – 8.4 | WordPress, Joomla, Drupal, Laravel and any PHP code |
| Node.js, Python, Go | Your own application: give its start command; it listens on `$PORT` |

Each website runs in its own container, as its own system user, with its own
memory and CPU limits (only administrators change those). Its files live in
`/var/lib/openplex/sites/<name>/`; the web server serves the **document
root**, `public/` by default.

Subdomains (`blog.example.com`) are websites of their own. **Aliases** are extra
names that show the same website (`www.example.com`).

## Applications: WordPress, Joomla, Drupal

When you create a website, choose an **Application**. You can also install one
later on an empty website, from **Application → Install an application** on its
overview.

1. Give the site title, the administrator's username and e-mail, and the
   administrator password (one is generated: copy it, OpenPlex does not keep
   it). WordPress and Drupal can be installed in English, Spanish or Catalan;
   Joomla in English (add languages from its administration).
2. OpenPlex switches the website to PHP, in a version the application supports
   (Joomla 6 and Drupal 11 need PHP 8.3 or later), and creates a MariaDB
   database for it, which counts towards the owner's database quota.
3. The latest release is downloaded from the project itself and installed in
   the document root. It takes one or two minutes; the **Application** card
   shows the progress, then the version, the database and a link to the
   administration.

The document root must be empty: the installer never overwrites files. If an
install fails, it removes the files it wrote and its database, and the card
says why; you can try again. Only the owner of the website (and the people who
manage that account) can install applications, because the database is
created in the owner's name.

WordPress may start in English for a few days after a new release, until the
translation is published; it then offers it under **Dashboard → Updates**.

### Updating an application

The **Application** card lists what can be updated: the application itself
and, for WordPress, its plugins and themes, for Drupal its modules. OpenPlex
checks once a day; **Check now** checks straight away. **Update** lets you
choose what to update and then:

1. backs up the website's files and the application's database (it appears in
   **Backups** marked *before an update*); if the backup fails, nothing is
   updated;
2. updates with the application's own tools: WP-CLI for WordPress (with the
   database upgrade and the translations), Joomla's command line for Joomla,
   Composer and Drush for Drupal (Drush is added to the project the first
   time; the website is in maintenance mode meanwhile, and a failed Composer
   run puts the previous code back);
3. checks that the website still answers. If it shows a server error, the
   card says the update failed: restore the backup from the website's
   **Backups** tab.

Notes:

- Joomla extensions are updated from Joomla's administration (**System →
  Update → Extensions**); the card lists them.
- Drupal is updated within the versions its `composer.json` allows: a new
  major version, or a module pinned to an exact version, is not offered.
- An update needs the website running and Internet access from the server; it
  usually takes under a minute (a few minutes for Drupal).
- Only the owner of the website (and the people who manage that account) can
  check and update, as with installs. You can still update from the
  application's own administration.

## PHP settings

On a PHP website, **Settings → PHP settings** changes these values. Empty
fields keep the default (shown in grey).

| Setting | Default | Limits |
|---|---|---|
| Memory per script (`memory_limit`) | 256M | 32M up to the website's memory |
| Largest file upload (`upload_max_filesize`) | 64M | up to 2048M |
| Largest form submission (`post_max_size`) | 64M | up to 2048M, at least the upload size |
| Script time limit (`max_execution_time`) | 60 s | 1 – 3600 |
| Time to read a request (`max_input_time`) | 60 s | 1 – 3600 |
| Form fields per request (`max_input_vars`) | 3000 | 100 – 100000 |
| Session lifetime (`session.gc_maxlifetime`) | 1440 s | 60 – 604800 |
| Show errors on the pages (`display_errors`) | Off | On / Off |
| Open remote URLs as files (`allow_url_fopen`) | On | On / Off |
| Time zone (`date.timezone`) | UTC | e.g. `Europe/Madrid` |
| Disabled functions (`disable_functions`) | none | comma-separated names |

Saving restarts the website's container (a few seconds). The values apply to
the website, its scheduled tasks and its web terminal alike. Keep
**Show errors** off on a live site: error messages can reveal paths and
settings to visitors.

## Files over SFTP

Besides the file manager (**Files** tab), every user can reach their websites'
files with an SFTP client such as FileZilla, WinSCP or Cyberduck. The details
are under **API & SSH keys → SFTP access**:

| | |
|---|---|
| Host | the panel's address (or the server's) |
| Port | 2222 |
| Username | your panel username |
| Password | an **SFTP password** set on that page, or one of your **SSH keys** |

The SFTP password is separate from the panel password, and cannot be the same:
SFTP clients store it, and it never opens the panel. With an SSH key, add its
public part under **SSH keys** on the same page.

After signing in you see one folder per website whose files you can manage:
your own websites, those of the accounts you manage, and websites shared with
you with the **Files** area (read-only if the share is). New files belong to
the website's user, as with the file manager. Links, moving files between
websites and running commands are not available.

On the first connection the client shows the server's key fingerprint:
compare it with the one on the **SFTP access** card. Repeated failed sign-ins
from an address ban it (Fail2Ban), like failed panel logins.

The server listens on port 2222 (`OPENPLEX_SFTP_LISTEN`, see
[installation](installation.md#configuration)); the panel's firewall keeps it
open. On a server that uses ufw instead, the installer opens it; if ufw still
blocks it (for example after updating from the panel), the **SFTP access**
card says so, and an administrator runs `ufw allow 2222/tcp` on the server.
