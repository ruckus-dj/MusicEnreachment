# docs/design

**Score: 9** (distinct domain: 7 files, authoritative conventions)

## OVERVIEW
Authoritative design documentation (Russian). Conventions, architecture, decisions, requirements, deployment model. This directory defines what is allowed and forbidden.

## WHERE TO LOOK
- decisions.md - approved stack, layering rules, config model, forbidden items (CORS, env vars beyond bootstrap, auto quality downgrades, GitHub snapshots for ffmpeg)
- repository-architecture.md - layer boundary rules: what each Go package may/may not do; layout patterns
- requirements.md - project requirements
- data-model.md - domain model and relationships
- deployment.md - "Do not move database between platforms expecting automatic tool migration"; server-only paths
- external-tools.md - tool integration requirements
- music_ingest_redesign.dbml - database schema design

## CONVENTIONS
**Documentation-first**: Conventions live here (Russian), not in inline comments. When code contradicts these docs, the docs are authoritative.

**Russian language**: Design docs are in Russian; code/comments/tests are English.

**Forbidden items catalog**: decisions.md explicitly lists what must NOT be added (CORS, new env vars, auth in-app, etc.).

**Immutable once shipped**: Changes to foundational decisions (config model, layering, auth-less design) require updating these docs first.

## STRUCTURE
designs.md and repository-architecture.md are the two most frequently referenced files. decisions.md covers what is allowed/forbidden; repository-architecture.md covers how packages relate.

## ANTI-PATTERNS
- Implementing features explicitly forbidden in decisions.md (CORS, new env vars, in-app auth)
- Violating layer boundaries documented in repository-architecture.md
- Adding dependencies not in approved stack
- Writing English design docs (Russian is the standard for docs/design/)
