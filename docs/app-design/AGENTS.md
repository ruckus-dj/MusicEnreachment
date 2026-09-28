# docs/app-design

**Score: 12** (distinct domain: 11 files, module boundary)

## OVERVIEW
Static HTML/JS design prototype with screenshots. NOT part of the React app; runs standalone via index.html. Used for early design exploration and UI reference.

## WHERE TO LOOK
- index.html - standalone prototype entry (not served by React app)
- prototype.js, catalog-data.js, theme.js - prototype logic and data
- styles.css - prototype styling
- 01-product-and-flows.md, 02-screens.md, 03-data-and-evidence.md - design docs
- DESIGN.md, QA.md, README.md - prototype documentation
- screenshots/ - versioned UI screenshots (v2/)

## CONVENTIONS
**Prototype coexistence**: This is a design artifact separate from the production React app in frontend/. The HTML prototype is NOT served by the backend's embedded static handler.

**Screenshot versioning**: screenshots/v2/ tracks design evolution.

**Design-first**: UI exploration happens here before React implementation.

## STRUCTURE
index.html is a standalone single-page app with:
- catalog-data.js: mock data for prototype
- prototype.js: UI state and interaction logic
- theme.js: color palette and design tokens
- styles.css: prototype-specific styling

Screenshots in screenshots/v2/ document the visual design decisions that informed the React implementation.

## NOTES
Open index.html directly in a browser to view the prototype. No build step, no server required. This prototype predates the React app and remains as design documentation.
