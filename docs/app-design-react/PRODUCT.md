# MeloTrove prototype

<!-- impeccable:product-schema 1 -->

## Platform

web

## Product purpose and users

MeloTrove manages a personal or small shared music library on a server, NAS,
or mounted disks through a desktop-oriented browser interface.
This standalone prototype demonstrates its workflows with demo data.

## Capabilities and constraints

The authoritative requirements remain in [the product contract](../design/decisions.md).
This document does not replace or duplicate that contract.
Preserve its source inventory, matching, metadata editing, publication,
operations, setup, and settings behavior.

The owner approved a full redesign of every prototype screen, including
reorganizing its presentation and navigation where useful. Preserve Russian
and English localization and both light and dark themes.
Use React Aria examples and accessible primitives where appropriate.

The prototype can lead the application while exploring UX. Bring
frontend-originated UI fixes back into the prototype without discarding its
newer work, as described in the [product contract](../design/decisions.md#интерфейс).
Demo data and providers remain outside `src/ui/`.

## Brand commitments

Keep the name MeloTrove. The owner selected the table in
https://react-aria.adobe.com/examples/crud as the UI and visual reference,
not the surrounding documentation website.
The owner also selected the React Aria GridList gallery pattern for the
library: artwork, title and description, search, multiple selection, and
keyboard navigation. Study the other component examples where appropriate;
do not force all screens into tables.

## Evidence

Existing demo fixtures and workflows are the product evidence. They are
demonstration data, not claims about a deployed user's library.
