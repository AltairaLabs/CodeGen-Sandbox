# Handoff: Codegen Sandbox docs — Atlas restyle

## Overview

`codegen-sandbox.altairalabs.ai` is an Astro **Starlight** docs site (theme: `starlight-theme-galaxy`)
whose only brand customisation today is an accent pin to a blue/cyan gradient. This handoff restyles it
into the **Atlas — AltairaLabs design system**, so it sits in the same sky as PromptKit, PromptArena,
Omnia and PromptPack.org.

Codegen Sandbox is a **component of Omnia that is also useful externally**. It therefore gets **no logo
of its own**: the header is the Omnia Star Tile mark + a "Codegen Sandbox" wordmark, and the site takes
the **Omnia register** — cool, starlight-led, instrument-grade, gold used once per view.

Repo: `AltairaLabs/CodeGen-Sandbox@main`, site source under `docs/`. See `github.md` for the
screen → source-file map.

## About the design files

The files in this bundle are **design references created in HTML** — a prototype showing the intended
look and behaviour. They are **not production code to copy**. The site is Astro + Starlight; the task is
to reproduce this design **inside Starlight's own theming surface** (CSS custom properties, component
overrides, MDX content), not to port the prototype's markup.

Two things in the bundle ARE directly usable:

- `starlight/custom.css` — a drop-in replacement for `docs/src/styles/custom.css` (see *Repo work*).
- `assets/` — real design-system SVGs (Omnia Star Tile, gold star glyph). Copy them, never redraw.

## Fidelity

**High-fidelity.** Colours, type, spacing, radii and states are final and come from Atlas tokens. Match
them by using the tokens, not by eyeballing the screenshots. Layout should be recreated pixel-close
within Starlight's grid; exact pixel parity with the prototype's hand-built shell is not the goal.

---

## ⚠ The family bar in the mock is NOT the real component

The slim top strip in the prototype is a **rough stand-in**, drawn only so the page composition reads
correctly. Its markup, dropdown contents, ordering, link set and metrics were sketched from
promptkit.altairalabs.ai and are **not the spec**.

**Implementation: import the shared `FamilyBar` from `atlas-components`.** Do not port the mock's markup.
Whatever the shared component renders is correct. Specifically discard the mock's:

- product switcher label and current-product marking
- in-house vs community grouping and ordering
- right-hand `Docs` / `Blog` links
- bar height, type sizes, and the `altairalabs.ai` lockup

Everything **below** the family bar (docs header, sidebar, content, footer) IS the design.

---

## Screens / views

All four screens share the shell: family bar → docs header (58px) → [sidebar 256px | content 1080px max | TOC 208px] → footer.

### 1. Introduction (splash / landing)

- **Purpose:** what the sandbox is, its tool surface, how it fits behind PromptKit.
- **Layout:** full-bleed hero band (`--atmo-starlight` radial over the canvas, 1px bottom hairline),
  `padding: 76px 48px 64px`, content max-width 1080px, two columns
  (`repeat(auto-fit, minmax(340px, 1fr))`, gap 48px, centred).
  Below: sections at 64px rhythm inside the same 1080px column with 48px side padding.
- **Components:**
  - **Eyebrow** — `MCP PROVIDER · PROMPTKIT CODEGEN`, mono 11px/1, `letter-spacing .14em`, uppercase, `var(--text-faint)`. Never gold.
  - **H1** — "The agent thinks. / The sandbox has the hands." Space Grotesk 600, 46px/1.06, `-0.03em`, `var(--text-heading)`.
  - **Tagline** — 20px/1.55, `var(--text-muted)`, max-width 44ch. Copy is the repo's own hero tagline.
  - **Actions** — primary `Getting started` (**the one gold element on the page**: gold fill `#E3B341`, ink text, radius 7px, `box-shadow: var(--glow-gold)`), secondary `Architecture` (starlight hairline outline, transparent).
  - **Hero chart panel** — 1px hairline, radius 16px, `var(--surface-1)`, padding 18px 16px; mono uppercase caption `brain / hands · one mcp wire`; an Atlas `ConstellationGraph` (470×240): entry `agent · brain` → branch `mcp wire` → three tool nodes (`filesystem`, `shell`, `verification`) → output `workspace`. This replaces the `hero-architecture.svg` illustration.
  - **Tool-surface grid** — 6 cards, `repeat(auto-fit, minmax(250px, 1fr))`, gap 16px. Each: `var(--surface-card)`, 1px hairline, radius 14px, padding 22px; a 6px node-colour dot + H3 (17px/1.2, 600) + body 14px/1.6 `var(--text-muted)`. Dot colours key to node kinds: filesystem `--node-agent`, search `--node-tool`, shell `--node-prompt`, verification `--status-healthy`, vendor MCP `--node-branch`, safe-by-default `--node-output`. Copy is `index.mdx`'s CardGrid verbatim.
  - **"Pull it, run it"** — Atlas `Terminal` (docs snippet register): `docker run` line, a `#` comment, a `✓` success line, one muted line. Gold appears only as the `❯` prompt.
  - **Next footer row** — hairline top rule, mono `next · getting started` left, link right.

### 2. Getting Started

- **Purpose:** fresh clone → agent-reachable sandbox in ~5 minutes.
- **Layout:** `padding: 52px 48px 96px`; article `flex: 1 1 480px` + TOC rail 208px, gap 48px, wrapping.
- **Components:** mono breadcrumb (`overview / getting started`, 12px, `--text-faint`); H1 40px/1.1; lead 20px/1.55 `--text-muted`, max 60ch; **prerequisites** as four hairline rows (radius 12px, `--surface-1`, padding 13px 16px) with an 88px mono key column (`go 1.25+`, `docker`, `on PATH`, `posix`); two Atlas `Terminal` blocks (local build, docker); a bash smoke-test **code surface** (`--surface-code`, radius 8px, mono 13px/1.75, a mono uppercase title strip with `copy` at the right); a **note callout** (starlight tint + `--starlight-border`, radius 12px, mono uppercase `note` label) for the vendor-MCP caveat; three "Next" link cards.
- **TOC rail:** mono uppercase `On this page` label, then items 13px with a 2px left rule — active `--accent-inter`, rest `--hairline`.

### 3. Architecture

- **Purpose:** the brain/hands split, the process model, layered defence, code intelligence.
- **Components:** the responsibility **table** (mono uppercase header row on `--surface-2`, 1.6fr/1fr grid, hairline-faint row rules, radius 14px); **process-model** `ConstellationGraph` (620×250) in a panel with a mono uppercase caption, plus a 4-item legend row (dot for node kinds, rotated square for the wire); **layered defence** as four numbered cards (`01`–`04` in mono `--text-faint`, 34px column + copy); **code-intelligence** `ConstellationGraph` (620×220) — `lsp tools` / `ast tools` → `internal/lsp` / `internal/ast` → `workspace.Resolve`.
- Both Mermaid flowcharts in `architecture.md` are replaced by these charts. Node labels must stay inside the viewBox — keep node labels short and put tool names in the prose.

### 4. Tool reference — Read (the pattern for all 32 tool pages)

- **Purpose:** the per-tool reference archetype.
- **Components:** mono breadcrumb; **H1 in mono** (tool names are machine data — `Read` set in Spline Sans Mono 40px/1.1) followed by two `Badge`s (`filesystem` / starlight, `claude-code shape` / neutral); lead 20px; **schema table** (5-column grid `1.1fr .7fr .7fr .6fr 2fr`, param + type + default cells in mono, `yes` in `--status-healthy-text`); **behaviour** as dotted rows (5px `--accent-node` dot, 15px/1.65); **example response** in a code surface with the title strip; **limits** as an amber callout (`--amber-tint` + `--amber-border`, mono uppercase `caution`); **related** as three mono-titled link cards.
- No gold anywhere on this page — gold is a per-view budget and a reference page has no single hero action.

---

## Interactions & behaviour

- **Sidebar nav** — Starlight owns routing; the prototype fakes it with state. Active item: `--accent-inter` text on `--starlight-tint`, radius 7px.
- **Family bar switcher** — shared component; behaviour is whatever it ships.
- **Day/night toggle** — sets `data-theme="light" | "dark"` on `<html>`. Atlas re-skins the whole tree from that single attribute; Starlight uses the same attribute, so its own `<ThemeSelect>` works unchanged. **Light is the default** (`<html data-theme="light">`).
- **Hover** — links gain a 1px starlight underline; hairlines strengthen `--hairline` → `--hairline-strong`; text lifts toward `--text-heading`. No scale transforms.
- **Motion** — `--dur-base` 200ms, `--ease-standard` `cubic-bezier(.4,0,.2,1)`; entrances `--ease-out` `cubic-bezier(.16,1,.3,1)`. The only decorative animation allowed is `atlas-twinkle` (3s) on a gold key star, and the terminal's `atlas-blink` cursor. Reduced motion removes entrances, never elements.
- **Responsive** — hero and card grids are `auto-fit minmax()`; the TOC rail wraps below the article under ~740px of content width. Sidebar collapses to Starlight's mobile drawer.
- **Loading / error / empty** — the docs site has none. If any collection view is added, an empty state is copy, not a gap (Atlas `EmptyState`).

## State management

Only the prototype needs state; Starlight supplies all of it in production.

| State | Trigger | Production owner |
|---|---|---|
| current page | sidebar / link click | Astro routing |
| `data-theme` | toggle click | Starlight `<ThemeSelect>` + `localStorage` |
| family-bar dropdown open | switcher click | shared `FamilyBar` component |

No data fetching.

## Design tokens

Do not hardcode these — link the Atlas token files and use `var(--*)`. Values listed so the mapping is auditable.

**Ink (dark canvas):** void `#070C16` · canvas `#0A1120` · surface `#0C1526` · raised `#111C2E` · hairline-solid `#1E3149`
**Starlight text ramp:** `#F4F8FF` `#F2F6FC` `#E6EDF8` `#DCE6F5` `#C7D5EA` `#9FB1CC` `#8195B0` `#73849F` `#5E708C` `#4A5A74`
**Leads:** Antique Gold `#E3B341` (gold text on light deepens to `#96690C`; text on gold fills `#06122A`) · Starlight Blue `#3B82F6` (→ `#2563EB` on light) · light starlight `#93C5FD`
**Support:** nebula violet `#C4B5FD` · ion cyan `#67E8F9` · pulsar green `#34D399` · amber `#F59E0B` · signal red `#EF4444`
**Hairlines:** default `rgba(147,197,253,.10)` · strong `.18` · faint `.06` · gold outline `rgba(227,179,65,.30)`
**Tints:** starlight `rgba(59,130,246,.12)` · gold `rgba(227,179,65,.10)` · status tints are `color-mix(… 10%)`
**Type:** Space Grotesk (400/500/600) + Spline Sans Mono (400/500). Sizes: hero 58 · h1 46 · h2 34 · h3 20 · lead 20 · body 16 · body-sm 14 · caption 13 · mono 14/13/12 · mono-label 11 · mono-micro 10. Tracking: hero `-0.03em` · tight `-0.02em` · snug `-0.01em` · label `.08em` · eyebrow `.14em` · wide `.18em`. Leading: hero 1.04 · head 1.12 · body 1.6.
**Spacing:** 4px scale (4 8 10 12 14 16 18 20 22 24 28 32 40 48 64 80 104). Section rhythm 64–80px, hero 76–104px, page gutter 48px, content 1080px / wide 1180px.
**Radii:** 4 (xs) · 6 (sm) · 7 (buttons) · 8 (code/panels) · 12 (small cards, inputs) · 14 (default card) · 16 (feature) · 999 (pills) · 25% (Star Tile).
**Shadows / glow:** `--shadow-card` `0 1px 0 rgba(147,197,253,.04), 0 8px 30px -16px rgba(0,0,0,.6)`; pop `0 18px 50px -20px rgba(0,0,0,.7)`. Glow is gold-only: primary button `0 8px 22px -8px rgba(227,179,65,.5)`; key star `0 0 8px`.
**Atmospheres:** `--atmo-starlight` (hero) and `--atmo-gold` / `--atmo-footer` radial blooms at 8–16% opacity over flat ink. No gradient meshes, no photography.

### The rules that must survive implementation

1. **Gold once per view.** On this site that is the landing hero's primary action. Not on eyebrows, not on nav, not on tool pages.
2. **Chrome never takes gold** — family bar, header, sidebar, TOC, callout labels, table headers.
3. **Machine data is lowercase mono**; uppercase mono is for eyebrows/labels only.
4. **No emoji, ever.** Status is a coloured dot + mono label.
5. **Deep ink, never black; observatory-white, never cream.**
6. **Charts, not decoration** — relationships are constellation graphs, not boxes-and-arrows clip art.

## Assets

| File | Source | Use |
|---|---|---|
| `assets/logo-omnia.svg` | Atlas `assets/logos/logo-omnia.svg` | Header mark (replaces `docs/public/logo.svg`) |
| `assets/star-glyph-gold.svg` | Atlas `assets/logos/star-glyph-gold.svg` | Footer / family-bar lockup |
| `_ds/…/assets/fonts/*.woff2` | Atlas | Space Grotesk + Spline Sans Mono (the brand's real webfonts, not substitutions) |
| `_ds/…/tokens/*.css` | Atlas | All tokens incl. the `[data-theme="light"]` scope |

Icons are thin-stroke line icons (~1.5–2px, rounded joins) — Lucide is the sanctioned CDN match. Filled glyphs are reserved for the star. The existing gradient-bars `logo.svg` and the `hero-architecture.svg` illustration are both retired.

## Repo work (Astro / Starlight)

1. **`docs/src/styles/custom.css`** → replace with `starlight/custom.css` from this bundle. It maps every `--sl-color-*` / `--sl-font-*` onto Atlas tokens, keeps starlight blue as the accent, and applies gold only to `.hero .action.primary`.
2. **Atlas tokens** → vendor the token CSS + woff2 fonts into `docs/src/styles/` + `docs/public/atlas/`, and list them before `custom.css` in `customCss` (PromptKit already serves its Atlas assets from `/atlas/…` — follow that convention).
3. **Light default** → set `data-theme="light"` on `<html>` (Starlight's `defaultTheme`) — Atlas is dark-first, this site opts into the printed-star-chart sky.
4. **Family bar** → mount the shared `atlas-components` `FamilyBar` above Starlight's header (a `PageFrame`/`Header` component override; the site already overrides `PageFrame` with `SitePageFrame.astro`).
5. **Header lockup** → point `logo.src` at the Omnia Star Tile; keep `title: 'Codegen Sandbox'` as the wordmark.
6. **Diagrams** → replace the Mermaid blocks in `index.mdx` and `architecture.md` with the Atlas `ConstellationGraph`. Node/edge data for all three charts is in the prototype's logic class (`heroNodes`/`heroEdges`, `procNodes`/`procEdges`, `intelNodes`/`intelEdges`). Once none remain, drop `mermaid` from `docs/package.json` and remove `docs/public/mermaid-init.js` from `head`.
7. **Terminals** → shell/install snippets become the Atlas `Terminal` register (docs snippets are an explicitly sanctioned use). Keep plain code surfaces for JSON/tool output.
8. **Sidebar** → structure is unchanged from `astro.config.mjs`; only styling changes.
9. **Galaxy theme** → the Atlas mapping supersedes the Galaxy accent pin. Keep `starlight-theme-galaxy` only if you still want its layout extras; otherwise remove it and its `--fb-*` variables.

## Files in this bundle

| Path | What it is |
|---|---|
| `Codegen Sandbox Docs.dc.html` | The design prototype — all four screens, live nav, day/night toggle. Open in a browser. |
| `support.js`, `_ds/` | Runtime + Atlas tokens/fonts/components the prototype loads. Keep alongside it. |
| `assets/` | The two brand SVGs the design uses. |
| `starlight/custom.css` | Drop-in replacement for `docs/src/styles/custom.css`. |
| `github.md` | Repo association + screen → source-file map. |
| `README.md` | This document. |
