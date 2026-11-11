# Licencia, extensiones y marca

[English](../en/licensing.md) · **Español** · [Català](../ca/licensing.md)

> Los textos legales son [`LICENSE`](../../LICENSE) (AGPL-3.0),
> [`LICENSE-EXCEPTION.md`](../../LICENSE-EXCEPTION.md) y
> [`TRADEMARKS.md`](../../TRADEMARKS.md), en inglés. Esta página los explica
> con palabras sencillas y no es vinculante; si algo difiere, mandan esos
> archivos.

## El núcleo: AGPL-3.0

El núcleo de OpenPlex (este repositorio) es software libre bajo la **GNU
Affero General Public License, versión 3 (solo esa versión)**.

- **Usarlo**, para ti, tu empresa o tus clientes, con fines comerciales o
  no, es libre y no necesita permiso.
- **Ofrecerlo sin modificar**, por ejemplo como imagen de servidor en un
  proveedor de hosting, está permitido; hay que conservar los avisos de la
  licencia y dar acceso al código fuente (basta con un enlace a este
  repositorio).
- **Modificarlo** está permitido. Si dejas que otros usen tu versión
  modificada, dándoles una copia **o a través de la red** como hace un
  proveedor de hosting, tienes que publicar el código de tus cambios bajo la
  AGPL-3.0, y no puedes llamarla "OpenPlex" (mira más abajo).

## Extensiones: cualquier licencia, también de pago

La [excepción para extensiones de OpenPlex](../../LICENSE-EXCEPTION.md) es un
permiso adicional sobre la AGPL: una **extensión** que funciona con OpenPlex
solo a través de su interfaz de extensiones documentada (la API HTTP, el
manifiesto y el paquete de extensión, el socket de la API de extensiones,
los eventos y las páginas de extensión: mira
[crear una extensión](extensions.md)) puede tener **cualquier licencia**, también de código cerrado y de
pago, aunque se instale y se ejecute junto con OpenPlex.

- Puedes crear y vender tus propias extensiones, o regalarlas.
- Una extensión que copia código fuente de OpenPlex queda, en esa parte,
  bajo la AGPL.
- Modificar el propio OpenPlex sigue cubierto por la AGPL.

## Extensiones oficiales

El proyecto OpenPlex publicará extensiones oficiales:

- **gratuitas y de código abierto**, en el repositorio público de extensiones;
- **de pago**, con pago único y licencia propia (número de servidores,
  actualizaciones y soporte se indican en la licencia de cada extensión).

## Marca

El código es libre; **el nombre y los logotipos no**. Puedes decir que tu
servicio "usa" OpenPlex o "es compatible con" OpenPlex y ofrecer versiones
sin modificar con ese nombre. Una versión modificada debe usar otro nombre, y
una extensión no puede presentarse como oficial si no lo es. Los detalles
están en [`TRADEMARKS.md`](../../TRADEMARKS.md).

## Contribuciones

Todavía no se aceptan contribuciones de código, así que el proyecto conserva
la propiedad completa del código. Cuando se abran, los contribuidores
firmarán un acuerdo de contribución (CLA). Mira
[`CONTRIBUTING.md`](../../CONTRIBUTING.md).
