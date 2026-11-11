# OpenPlex Extension Exception

> **Draft — to be reviewed by a lawyer before the first public release.**
> This English text is the only legally binding version. Translations in
> `docs/es/licensing.md` and `docs/ca/licensing.md` are for information only.

OpenPlex is licensed under the GNU Affero General Public License, version 3
only (the "AGPL"; see `LICENSE`). As an additional permission under section 7
of the AGPL, the copyright holders of OpenPlex (Avakiam and, where
they exist, other contributors) grant you the following
exception.

## 1. Definitions

- **"OpenPlex"** means the software in this repository, as published by its
  copyright holders, and any modified version of it.
- **"Extension Interface"** means the interfaces that the copyright holders
  document as intended for extensions, in the OpenPlex documentation for the
  version you use. At the time of writing these are: the HTTP API served under
  `/api/` and the WebSocket endpoints it documents; the extension manifest
  (`openplex-extension.json`) and package format (`.opx`); the per-extension
  API socket; the events OpenPlex sends to an extension; and the pages and
  website tabs OpenPlex displays from an extension, together with the request
  headers it adds to them, all as described in the extension documentation.
- **"Extension"** means a work that adds functionality to OpenPlex and that
  interacts with OpenPlex only through the Extension Interface, for example a
  program running in its own process or container that calls the HTTP API.
  An Extension may include files that the Extension Interface requires it to
  contain (such as a manifest), and may be loaded, installed, configured,
  started or displayed by OpenPlex through the Extension Interface.

## 2. Permission

You may create, use, convey, sell, license and offer over a network an
Extension under terms of your choice, including proprietary terms and
one-time or recurring fees, without the Extension or its source code becoming
subject to the AGPL, even where OpenPlex and the Extension are distributed,
installed or run together.

## 3. Conditions

1. This permission does not apply to any part of an Extension that copies or
   is derived from the source code of OpenPlex beyond what the Extension
   Interface requires (for example names of API fields or manifest keys). Such
   parts remain subject to the AGPL.
2. OpenPlex itself, including any modification of it, remains subject to the
   AGPL. If you modify OpenPlex and let users interact with the modified
   version over a network, section 13 of the AGPL applies to the modified
   OpenPlex, but not to Extensions that meet this exception.
3. An Extension must not remove, hide or bypass OpenPlex's AGPL notices or the
   offer of its Corresponding Source to users.
4. This exception does not grant any right to use the OpenPlex name or logos;
   see `TRADEMARKS.md`.

## 4. Removing the exception

If you modify OpenPlex, you may extend this exception to your version, but
you are not obliged to do so. If you do not wish to, delete this file and the
reference to it from your version.
