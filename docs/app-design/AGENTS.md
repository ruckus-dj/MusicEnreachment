# docs/app-design

**Score: 12** (distinct domain: 11 files, module boundary)

## OVERVIEW
Static interactive HTML/JS design prototype. It is the sole graphical reference, NOT part of the React app, and runs standalone via index.html.

## WHERE TO LOOK
- index.html - standalone prototype entry (not served by React app)
- prototype.js, catalog-data.js, theme.js - prototype logic and data
- manual-release-proposal.js - manual-release form mounted by the main hash router
- styles.css - prototype styling
- 01-product-and-flows.md, 02-screens.md, 03-data-and-evidence.md - design docs
- DESIGN.md, QA.md, README.md - prototype documentation

## CONVENTIONS
**Prototype coexistence**: This is a design artifact separate from the production React app in frontend/. The HTML prototype is NOT served by the backend's embedded static handler.

**Visual reference**: use the live interactive prototype in index.html and its JS/CSS. Do not store or regenerate screenshot artifacts for routine changes. Browser-based functional and visual inspection remains appropriate without saved screenshot artifacts or a screenshot matrix.

**Design-first**: UI exploration happens here before React implementation.

**Keep the live design synchronized**: when approved requirements change visible
behavior, update the written contract and the interactive HTML/JS/CSS prototype
together. A Markdown-only update is not a graphical-design update. Submit the
live prototype for the owner's visual approval, without screenshot artifacts.

## STRUCTURE
index.html is a standalone single-page app with:
- catalog-data.js: mock catalog data for prototype
- prototype.js: UI state and interaction logic
- manual-release-demo-data.js: manual-form options derived from the shared mock catalog
- manual-release-proposal.js: manual form mounted into the main `#view`
- theme.js: color palette and design tokens
- styles.css: prototype-specific styling

The interactive prototype in index.html, together with its JS/CSS, is the sole graphical design reference. Written product decisions remain authoritative for product requirements.

## NOTES
Open index.html directly in a browser to view the prototype. No build step, no server required. This prototype predates the React app and remains as design documentation.
