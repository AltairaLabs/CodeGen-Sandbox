# Atlas Docs Restyle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the `codegen-sandbox.altairalabs.ai` Starlight docs site into the Atlas (AltairaLabs) design system, building the home page to the authoritative design and letting all other pages inherit the styling.

**Architecture:** Atlas design tokens are vendored into `docs/src/styles/atlas/` and loaded ahead of everything else in `customCss`, so every downstream stylesheet resolves `var(--*)` against them. `docs/src/styles/custom.css` becomes a pure mapping layer that re-points Starlight's own `--sl-*` tokens onto Atlas tokens — this is what restyles all 48 non-home pages with zero content edits. The home page additionally gets two new Astro components (`ConstellationGraph.astro`, `Terminal.astro`) and a `Hero.astro` override, because Starlight's splash hero has no slot for an eyebrow or a chart panel.

**Tech Stack:** Astro 6, Starlight 0.38, `starlight-theme-galaxy`, `@altairalabs/brand` (family bar), Mermaid 11, plain CSS custom properties. No React, no build-time chart library.

**Spec:** `docs/plans/2026-08-15-codegen-sandbox-docs-atlas-handoff.md` (the design handoff, copied verbatim from the delivered bundle)

**Design bundle (source of assets):** `~/Downloads/Codegen Sandbox design system update.zip` → `design_handoff_codegen_sandbox_docs/`. Referred to below as `$BUNDLE`. Set it once per shell:

```bash
export BUNDLE="$HOME/Downloads/design_handoff_codegen_sandbox_docs"
```

If that directory does not exist, re-extract it:

```bash
unzip -o "$HOME/Downloads/Codegen Sandbox design system update.zip" -d "$HOME/Downloads"
```

---

## Global Constraints

These apply to every task. They come from the spec's "The rules that must survive implementation" plus the scope decisions taken during review.

**Scope (decided during handoff review — this overrides the spec where they differ):**

- **Only the home page (`index.mdx`) is authoritative.** Getting Started, Architecture and the Read tool reference in the spec are *indicative*. They must map onto the site's existing page types and receive styling only — **no content restructuring, no new frontmatter, no `.md` → `.mdx` conversions** on those pages.
- The site has exactly **two page types**: the splash page (`index.mdx`, the only file with `template: splash`) and the plain doc page (the other 48 files, all of which carry only `title` + `description` frontmatter). There are **zero** component imports and **zero** `:::` asides outside `index.mdx`. Do not introduce either.
- **Spec item 4 (the family bar) is already done.** `docs/src/components/Header.astro` mounts `FamilyBar` from `@altairalabs/brand`, and `docs/src/components/SitePageFrame.astro` overrides `PageFrame`. Do not re-implement, do not port the mock's markup, do not install a second package. The spec calls it `atlas-components`; in this repo it is `@altairalabs/brand`.
- Spec items that would require touching indicative pages are **explicitly out of scope**: tool-page `Badge`s, the amber `caution` callout, the Getting Started `note` callout, mono breadcrumbs, and prerequisites-as-hairline-rows.
- **Mermaid stays.** Spec item 6 says drop it; that assumed all four charts were being converted. `architecture.md` keeps its two flowcharts, so the dependency and `mermaid-init.js` remain — re-themed to Atlas (Task 8).
- **"Defence in depth" stays on the home page** as a Mermaid block, contrary to the spec, which relocates it to Architecture. This is a deliberate see-how-it-looks decision.
- **"How it fits"** loses its Mermaid block — the hero `ConstellationGraph` carries the same content (agent → MCP wire → tools → workspace). Its prose paragraph stays.

**Design rules (from the spec, verbatim):**

1. **Gold once per view.** On this site that is the landing hero's primary action.
2. **Chrome never takes gold** — family bar, header, sidebar, TOC, callout labels, table headers.
3. **Machine data is lowercase mono**; uppercase mono is for eyebrows/labels only.
4. **No emoji, ever.** Status is a coloured dot + mono label. (`❯`, `✓`, `→` are symbols, not emoji, and are permitted.)
5. **Deep ink, never black; observatory-white, never cream.**
6. **Charts, not decoration.**

**Known tension — do not "fix" it, flag it at review:** rules 1 and 2 say gold appears once per view, but the Atlas `ConstellationGraph` renders `entry` and `output` nodes in gold with a twinkle, and the Atlas `Terminal` renders its `❯` prompt and cursor in gold. The spec explicitly specifies both components for the home page *and* sanctions the key-star glow. Implement the components to their extracted spec; raise the count at review rather than deviating.

**Technical constraints:**

- Never hardcode a colour, size, radius or duration that exists as an Atlas token. Use `var(--*)`.
- Atlas token CSS must be listed in `customCss` **before** `@altairalabs/brand/family-bar-starlight.css` and `./src/styles/custom.css`.
- **Light is the default theme** (`data-theme="light"`). Atlas is dark-first; this site opts into the light "printed star chart" sky. Both themes must work.
- Conventional commits, signed off (`git commit -s`).
- Work on branch `feat/docs-atlas-restyle`. Do not open a PR until explicitly asked.

**Verification commands** (this is a docs site — there is no unit-test harness, so verification is build output plus a browser check):

```bash
npm --prefix docs install        # once, if node_modules is absent
npm --prefix docs run build      # must exit 0
npm --prefix docs run check-links
npm --prefix docs run dev        # http://localhost:4321 for visual checks
```

---

### Task 0: Branch setup

**Files:** none

- [ ] **Step 1: Create the working branch**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox checkout -b feat/docs-atlas-restyle
```

- [ ] **Step 2: Install docs dependencies and confirm a clean baseline build**

```bash
npm --prefix docs install
npm --prefix docs run build
```

Expected: build exits 0. If it fails, stop and report — the baseline is broken and nothing below is meaningful.

- [ ] **Step 3: Commit the spec copy already staged in the repo**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/plans/2026-08-15-codegen-sandbox-docs-atlas-handoff.md docs/plans/2026-08-15-codegen-sandbox-docs-atlas.md
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "docs(plans): add Atlas docs restyle plan and design handoff spec"
```

---

### Task 1: Vendor Atlas tokens and fonts

The spec's `tokens/fonts.css` is **broken as shipped** — it declares eight static per-weight `@font-face` rules pointing at filenames (`space-grotesk-400.woff2` … `spline-sans-mono-700.woff2`) that are not in the bundle. The bundle ships **five variable fonts** (`wght` axis 300–700) subset by unicode-range. Using it unchanged silently falls back to system fonts. This task rewrites it.

**Files:**
- Create: `docs/public/atlas/fonts/` (5 `.woff2` files, copied)
- Create: `docs/src/styles/atlas/colors.css`, `typography.css`, `spacing.css`, `effects.css`, `theme-light.css` (copied verbatim)
- Create: `docs/src/styles/atlas/fonts.css` (**rewritten**, not copied)
- Modify: `docs/astro.config.mjs:26` (the `customCss` array)

**Interfaces:**
- Produces: the full Atlas custom-property surface — `--ink-*`, `--star-*`, `--gold-*`, `--starlight-*`, `--node-*`, `--surface-*`, `--text-*`, `--hairline*`, `--radius-*`, `--tracking-*`, `--font-sans`, `--font-mono`, `--atmo-*`, `--glow-gold`, `--shadow-card`, `--dur-*`, and the `atlas-twinkle` / `atlas-blink` keyframes. Every later task consumes these.

- [ ] **Step 1: Verify the failure first — confirm no Atlas token resolves today**

```bash
npm --prefix docs run build
grep -rc -- "--gold-500" docs/dist/ | grep -v ':0' | head
```

Expected: no output (exit 1 from grep). Atlas tokens are absent from the built site.

- [ ] **Step 2: Copy the font files**

```bash
mkdir -p docs/public/atlas/fonts
cp "$BUNDLE"/_ds/atlas-*/assets/fonts/*.woff2 docs/public/atlas/fonts/
ls docs/public/atlas/fonts/
```

Expected exactly these five files:

```
space-grotesk-latin-ext.woff2
space-grotesk-latin.woff2
space-grotesk-vietnamese.woff2
spline-sans-mono-latin-ext.woff2
spline-sans-mono-latin.woff2
```

- [ ] **Step 3: Copy the five token files that need no changes**

```bash
mkdir -p docs/src/styles/atlas
cp "$BUNDLE"/_ds/atlas-*/tokens/colors.css      docs/src/styles/atlas/colors.css
cp "$BUNDLE"/_ds/atlas-*/tokens/typography.css  docs/src/styles/atlas/typography.css
cp "$BUNDLE"/_ds/atlas-*/tokens/spacing.css     docs/src/styles/atlas/spacing.css
cp "$BUNDLE"/_ds/atlas-*/tokens/effects.css     docs/src/styles/atlas/effects.css
cp "$BUNDLE"/_ds/atlas-*/tokens/theme-light.css docs/src/styles/atlas/theme-light.css
```

Do **not** copy `tokens/fonts.css` — Step 4 replaces it.

- [ ] **Step 4: Write the corrected `docs/src/styles/atlas/fonts.css`**

```css
/* Atlas — self-hosted fonts (OFL).
 * Space Grotesk = interface; Spline Sans Mono = machine data.
 *
 * NOTE: this file deliberately differs from the design bundle's
 * tokens/fonts.css, which declares eight static per-weight faces pointing at
 * filenames the bundle does not ship. The delivered fonts are VARIABLE
 * (wght axis 300-700), subset by unicode-range — five files, five faces.
 *
 * Served from docs/public/atlas/fonts/ → /atlas/fonts/... at runtime.
 */

@font-face {
  font-family: 'Space Grotesk';
  font-style: normal;
  font-weight: 300 700;
  font-display: swap;
  src: url('/atlas/fonts/space-grotesk-latin.woff2') format('woff2');
  unicode-range: U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA,
    U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+2074, U+20AC, U+2122, U+2191,
    U+2193, U+2212, U+2215, U+FEFF, U+FFFD;
}

@font-face {
  font-family: 'Space Grotesk';
  font-style: normal;
  font-weight: 300 700;
  font-display: swap;
  src: url('/atlas/fonts/space-grotesk-latin-ext.woff2') format('woff2');
  unicode-range: U+0100-02BA, U+02BD-02C5, U+02C7-02CC, U+02CE-02D7, U+02DD-02FF,
    U+0304, U+0308, U+0329, U+1D00-1DBF, U+1E00-1E9F, U+1EF2-1EFF, U+2020,
    U+20A0-20AB, U+20AD-20C0, U+2113, U+2C60-2C7F, U+A720-A7FF;
}

@font-face {
  font-family: 'Space Grotesk';
  font-style: normal;
  font-weight: 300 700;
  font-display: swap;
  src: url('/atlas/fonts/space-grotesk-vietnamese.woff2') format('woff2');
  unicode-range: U+0102-0103, U+0110-0111, U+0128-0129, U+0168-0169, U+01A0-01A1,
    U+01AF-01B0, U+0300-0301, U+0303-0304, U+0308-0309, U+0323, U+0329,
    U+1EA0-1EF9, U+20AB;
}

@font-face {
  font-family: 'Spline Sans Mono';
  font-style: normal;
  font-weight: 300 700;
  font-display: swap;
  src: url('/atlas/fonts/spline-sans-mono-latin.woff2') format('woff2');
  unicode-range: U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA,
    U+02DC, U+0304, U+0308, U+0329, U+2000-206F, U+2074, U+20AC, U+2122, U+2191,
    U+2193, U+2212, U+2215, U+FEFF, U+FFFD;
}

@font-face {
  font-family: 'Spline Sans Mono';
  font-style: normal;
  font-weight: 300 700;
  font-display: swap;
  src: url('/atlas/fonts/spline-sans-mono-latin-ext.woff2') format('woff2');
  unicode-range: U+0100-02BA, U+02BD-02C5, U+02C7-02CC, U+02CE-02D7, U+02DD-02FF,
    U+0304, U+0308, U+0329, U+1D00-1DBF, U+1E00-1E9F, U+1EF2-1EFF, U+2020,
    U+20A0-20AB, U+20AD-20C0, U+2113, U+2C60-2C7F, U+A720-A7FF;
}
```

- [ ] **Step 5: Wire the tokens into `customCss`, ahead of everything else**

In `docs/astro.config.mjs`, replace line 26:

```js
      customCss: ['@altairalabs/brand/family-bar-starlight.css', './src/styles/custom.css'],
```

with:

```js
      customCss: [
        // Atlas design tokens first — every stylesheet below resolves var(--*)
        // against these. Order within the group matters: fonts and colors
        // define what typography/effects/theme-light reference.
        './src/styles/atlas/fonts.css',
        './src/styles/atlas/colors.css',
        './src/styles/atlas/typography.css',
        './src/styles/atlas/spacing.css',
        './src/styles/atlas/effects.css',
        './src/styles/atlas/theme-light.css',
        '@altairalabs/brand/family-bar-starlight.css',
        './src/styles/custom.css',
      ],
```

- [ ] **Step 6: Rebuild and verify the tokens and fonts reached the output**

```bash
npm --prefix docs run build
grep -rl -- "--gold-500" docs/dist/ | head -3
ls docs/dist/atlas/fonts/
```

Expected: at least one built CSS file matches `--gold-500`, and all five `.woff2` files are present under `docs/dist/atlas/fonts/`.

- [ ] **Step 7: Verify no `@font-face` points at a missing file**

```bash
grep -rho "/atlas/fonts/[a-z-]*\.woff2" docs/dist/ | sort -u | while read -r p; do
  test -f "docs/dist${p}" && echo "OK   ${p}" || echo "MISS ${p}"
done
```

Expected: five `OK` lines, zero `MISS`.

- [ ] **Step 8: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/public/atlas docs/src/styles/atlas docs/astro.config.mjs
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): vendor Atlas design tokens and variable webfonts"
```

---

### Task 2: Map Starlight onto Atlas, default to light

**Files:**
- Modify: `docs/src/styles/custom.css` (full replacement)
- Modify: `docs/astro.config.mjs` (add `defaultTheme`)

**Interfaces:**
- Consumes: every Atlas token from Task 1.
- Produces: the restyled baseline for all 49 pages. Later tasks add to `custom.css`; they never replace it.

- [ ] **Step 1: Confirm the current accent is still the old blue/cyan**

```bash
grep -n "hsl(199" docs/src/styles/custom.css
```

Expected: matches on the Galaxy accent pins. These are what Task 2 removes.

- [ ] **Step 2: Replace `docs/src/styles/custom.css` with the Atlas mapping**

Copy the bundle's drop-in verbatim — it is production-ready and already audited against the vendored tokens:

```bash
cp "$BUNDLE"/starlight/custom.css docs/src/styles/custom.css
```

Then edit its header comment block (lines 8–14) so it describes the real load order rather than the bundle's suggested one:

```css
/* ============================================================
   Codegen Sandbox docs — Atlas (AltairaLabs design system)

   Maps Starlight's design tokens onto Atlas tokens. Light is the
   default sky here ("the printed star chart"); dark is the night sky.

   The Atlas token files are vendored under ./atlas/ and listed ahead
   of this file in docs/astro.config.mjs customCss.
   ============================================================ */
```

- [ ] **Step 3: Set light as the default theme**

In `docs/astro.config.mjs`, inside the `starlight({...})` options, immediately after the `description` field, add:

```js
      // Atlas is dark-first; this site opts into the light "printed star
      // chart" sky. Starlight's ThemeSelect still toggles data-theme, which
      // is the same attribute Atlas re-skins from.
      defaultTheme: 'light',
```

- [ ] **Step 4: Build and confirm the old accent is gone**

```bash
npm --prefix docs run build
grep -rc "hsl(199" docs/dist/ | grep -v ':0'
```

Expected: no output. The blue/cyan Galaxy pin no longer appears anywhere in the built site.

- [ ] **Step 5: Visual check in both themes**

```bash
npm --prefix docs run dev
```

Open `http://localhost:4321/getting-started/` and confirm:
- Page loads **light** by default.
- Body text is Space Grotesk, code and inline `code` are Spline Sans Mono (not a system fallback — check DevTools → Computed → font-family, and Network → the `.woff2` requests return 200).
- Tables have a hairline border, 14px radius, and an uppercase mono header row on a tinted background.
- Toggling to dark via Starlight's theme control re-skins the whole page with no unstyled flash.

Then open `http://localhost:4321/tools/read/` and confirm the `## Schema` table renders with the Atlas grid — this is the single check that proves all 22 tool pages are covered.

- [ ] **Step 6: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/src/styles/custom.css docs/astro.config.mjs
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): map Starlight tokens onto Atlas, default to the light sky"
```

---

### Task 3: Tool-page schema table polish

The spec's Read page renders param / type / default cells in mono and `yes` in healthy-green. The existing markdown tables are already `| Param | Type | Required | Default | Notes |` — exactly the five columns the spec's grid describes — so this is reachable with `nth-child` rules and **no content edits**.

**Files:**
- Modify: `docs/src/styles/custom.css` (append)

**Interfaces:**
- Consumes: Atlas tokens; the table rules from Task 2.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Confirm the column contract holds across the tool pages**

```bash
grep -rh "^| Param" docs/src/content/docs/tools/ | sort | uniq -c
```

Expected: the dominant form is `| Param | Type | Required | Default | Notes |`, with a minority using the 4-column variant (no `Notes`). Both are handled below.

- [ ] **Step 2: Append the rules to `docs/src/styles/custom.css`**

```css
/* ---- Tool-reference schema tables --------------------------------
 * The tools/ pages all use `| Param | Type | Required | Default | Notes |`
 * (a few drop Notes). Machine data goes mono; an affirmative Required
 * reads as healthy. Scoped to tables that start with a Param column so
 * prose tables elsewhere are untouched.
 * ------------------------------------------------------------------ */
.sl-markdown-content table td:nth-child(1),
.sl-markdown-content table td:nth-child(2),
.sl-markdown-content table td:nth-child(4) {
  font-family: var(--font-mono);
  font-size: 13px;
}

/* `yes` in the Required column reads as a healthy signal, not as prose. */
.sl-markdown-content table td:nth-child(3) {
  font-family: var(--font-mono);
  font-size: 13px;
  color: var(--status-healthy-text);
}
```

- [ ] **Step 3: Build and check visually**

```bash
npm --prefix docs run build
npm --prefix docs run dev
```

Open `http://localhost:4321/tools/read/`, `/tools/edit/` and `/tools/bash/`. Confirm the schema tables show mono param/type/default cells and a green `yes`. Then open `http://localhost:4321/concepts/language-support/` and `/concepts/multi-workspace/` and confirm their prose tables still read as prose and have not been mangled.

If a prose table elsewhere is damaged by these rules, narrow the selector so it only matches tables whose first header cell is literally `Param`. Replace both rule blocks above with this scoped form and re-run the same visual check:

Wrap the whole Step 2 block in a route guard so it only applies on tool pages. Add `data-route` to the page shell first — in `docs/src/components/SitePageFrame.astro`, pass the slug down:

```astro
---
import Default from '@astrojs/starlight/components/PageFrame.astro';
import AltairaFooter from './AltairaFooter.astro';

const slug = Astro.locals.starlightRoute?.id ?? '';
---

<div data-route={slug.startsWith('tools/') ? 'tools' : 'doc'} style="display:contents">
  <Default {...Astro.props}>
    <slot name="header" slot="header" />
    <slot name="sidebar" slot="sidebar" />
    <slot />
  </Default>
</div>
<AltairaFooter />
```

then prefix each selector in Step 2 with `[data-route='tools']`. Report which of the two you ended up with.

- [ ] **Step 4: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/src/styles/custom.css
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): set tool-page schema tables in mono with healthy Required"
```

---

### Task 4: The `ConstellationGraph` Astro component

A faithful port of the Atlas component, extracted from the bundle's `_ds_bundle.js` (`components/viz/ConstellationGraph.jsx`). The production Atlas version is an interactive react-flow graph; the design-system version specified here is a **static SVG star chart** with hand-placed `x`/`y` in viewBox units. That is what the site needs, and it means no React in the docs build.

**Files:**
- Create: `docs/src/components/ConstellationGraph.astro`

**Interfaces:**
- Produces: `<ConstellationGraph nodes={…} edges={…} width={470} height={240} showLabels />`
  - `nodes`: `Array<{ id: string; x: number; y: number; kind: 'entry'|'output'|'branch'|'agent'|'tool'|'prompt'|'terminator'; label?: string }>`
  - `edges`: `Array<{ from: string; to: string; dashed?: boolean; gold?: boolean }>`
  - `width`, `height`: numbers, viewBox units. `showLabels`: boolean.
- Consumed by: Task 6 (`Hero.astro`).

- [ ] **Step 1: Write the component**

```astro
---
/**
 * ConstellationGraph — the Atlas signature pattern. Topologies as star
 * charts: nodes by kind, wayfinding lines (dashed for else-branches),
 * gold for entry and output.
 *
 * Ported from the Atlas design-system component
 * (components/viz/ConstellationGraph.jsx) in the design handoff bundle.
 * The production Atlas component is an interactive react-flow graph with
 * dagre layout; this is the static preview variant — pass x/y in viewBox
 * units. Keeping it static is deliberate: it keeps React out of the docs
 * build for what is a decorative-free, non-interactive diagram.
 */
interface Node {
  id: string;
  x: number;
  y: number;
  kind?: 'entry' | 'output' | 'branch' | 'agent' | 'tool' | 'prompt' | 'terminator';
  label?: string;
}
interface Edge {
  from: string;
  to: string;
  dashed?: boolean;
  gold?: boolean;
}
interface Props {
  nodes?: Node[];
  edges?: Edge[];
  width?: number;
  height?: number;
  showLabels?: boolean;
  title?: string;
}

const {
  nodes = [],
  edges = [],
  width = 360,
  height = 160,
  showLabels = false,
  title,
} = Astro.props as Props;

const KIND = {
  prompt: { fill: 'var(--node-prompt)', halo: 'rgba(196,181,253,0.16)', r: 4.5 },
  agent: { fill: 'var(--node-agent)', halo: 'rgba(147,197,253,0.14)', r: 4.5 },
  tool: { fill: 'var(--node-tool)', halo: 'rgba(103,232,249,0.16)', r: 4.5 },
  branch: { fill: 'var(--node-branch)', halo: 'rgba(227,179,65,0.18)', r: 4, diamond: true },
  output: { fill: 'var(--gold-500)', halo: 'rgba(227,179,65,0.16)', r: 5.5, glow: true },
  entry: { fill: 'var(--gold-500)', halo: 'rgba(227,179,65,0.16)', r: 5.5, glow: true },
  terminator: { fill: 'transparent', halo: 'transparent', r: 4, ring: true },
} as const;

const byId = Object.fromEntries(nodes.map((n) => [n.id, n]));
const kindOf = (n: Node) => KIND[n.kind ?? 'agent'] ?? KIND.agent;
---

<svg
  viewBox={`0 0 ${width} ${height}`}
  width="100%"
  preserveAspectRatio="xMidYMid meet"
  style="display:block"
  role="img"
  aria-label={title}
>
  {title && <title>{title}</title>}
  {
    edges.map((e) => {
      const a = byId[e.from];
      const b = byId[e.to];
      if (!a || !b) return null;
      const gold = b.kind === 'output' || e.gold;
      return (
        <line
          x1={a.x}
          y1={a.y}
          x2={b.x}
          y2={b.y}
          stroke={gold ? 'var(--gold-500)' : 'var(--starlight-500)'}
          stroke-width={1.2}
          opacity={gold ? 0.55 : 0.45}
          stroke-dasharray={e.dashed ? '3 5' : undefined}
        />
      );
    })
  }
  {
    nodes.map((n) => {
      const k = kindOf(n);
      const { x: cx, y: cy } = n;
      return (
        <g>
          {'diamond' in k && k.diamond ? (
            <rect
              x={cx - k.r * 2.2}
              y={cy - k.r * 2.2}
              width={k.r * 4.4}
              height={k.r * 4.4}
              rx="2"
              transform={`rotate(45 ${cx} ${cy})`}
              fill={k.halo}
            />
          ) : (
            <circle cx={cx} cy={cy} r={k.r * 2.4} fill={k.halo} />
          )}
          {'ring' in k && k.ring ? (
            <circle cx={cx} cy={cy} r={k.r} fill="none" stroke="var(--hairline-strong)" stroke-width="1.5" />
          ) : 'diamond' in k && k.diamond ? (
            <rect
              x={cx - k.r}
              y={cy - k.r}
              width={k.r * 2}
              height={k.r * 2}
              transform={`rotate(45 ${cx} ${cy})`}
              fill={k.fill}
            />
          ) : (
            <circle cx={cx} cy={cy} r={k.r} fill={k.fill} class={'glow' in k && k.glow ? 'atlas-key-star' : undefined} />
          )}
          {showLabels && n.label && (
            <text x={cx} y={cy + k.r * 2.4 + 12} text-anchor="middle" class="atlas-node-label">
              {n.label}
            </text>
          )}
        </g>
      );
    })
  }
</svg>

<style>
  .atlas-node-label {
    font: 500 9px var(--font-mono);
    fill: var(--star-900);
  }

  /* The one sanctioned decorative animation: the gold key star breathes.
     atlas-twinkle is defined in the vendored Atlas tokens/effects.css. */
  .atlas-key-star {
    filter: drop-shadow(0 0 6px rgba(227, 179, 65, 0.9));
    animation: atlas-twinkle var(--twinkle, 3s) ease-in-out infinite;
  }

  @media (prefers-reduced-motion: reduce) {
    .atlas-key-star {
      animation: none;
    }
  }
</style>
```

- [ ] **Step 2: Verify it compiles and renders the expected geometry**

Temporarily append this to `docs/src/content/docs/index.mdx` (it will be removed in Task 7):

```mdx
import ConstellationGraph from '../../components/ConstellationGraph.astro';

<ConstellationGraph
  width={470}
  height={240}
  showLabels
  title="Test render"
  nodes={[
    { id: 'brain', x: 46, y: 120, kind: 'entry', label: 'agent · brain' },
    { id: 'wire', x: 160, y: 120, kind: 'branch', label: 'mcp wire' },
    { id: 'fs', x: 290, y: 44, kind: 'tool', label: 'filesystem' },
    { id: 'sh', x: 290, y: 120, kind: 'tool', label: 'shell' },
    { id: 'vf', x: 290, y: 196, kind: 'tool', label: 'verification' },
    { id: 'vol', x: 424, y: 120, kind: 'output', label: 'workspace' },
  ]}
  edges={[
    { from: 'brain', to: 'wire' },
    { from: 'wire', to: 'fs' }, { from: 'wire', to: 'sh' }, { from: 'wire', to: 'vf' },
    { from: 'fs', to: 'vol' }, { from: 'sh', to: 'vol' }, { from: 'vf', to: 'vol' },
  ]}
/>
```

```bash
npm --prefix docs run build
grep -o "<line" docs/dist/index.html | wc -l
grep -o "atlas-node-label" docs/dist/index.html | wc -l
```

Expected: 7 `<line>` elements (one per edge) and 6 label elements (one per node).

- [ ] **Step 3: Revert the temporary test block**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox checkout docs/src/content/docs/index.mdx
```

- [ ] **Step 4: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/src/components/ConstellationGraph.astro
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): add the Atlas ConstellationGraph star-chart component"
```

---

### Task 5: The `Terminal` Astro component

Ported from the bundle's `components/tui/Terminal.jsx`. Used once, on the home page, for the "Pull it, run it" section.

**Files:**
- Create: `docs/src/components/Terminal.astro`

**Interfaces:**
- Produces: `<Terminal lines={…} title="codegen-sandbox · local" prompt="❯" />`
  - `lines`: `Array<{ type: 'command'|'comment'|'success'|'error'|'warn'|'muted'|'output'|'flag'|'path'; text: string }>`
  - `title`: optional string (renders the title strip with three dots, the third gold). `prompt`: string, default `'omnia ❯'`. `cursor`: boolean, default `true`.
- Consumed by: Task 7 (`index.mdx`).

- [ ] **Step 1: Write the component**

```astro
---
/**
 * Terminal — the Atlas TUI register. Ported from the design handoff bundle
 * (components/tui/Terminal.jsx). Used for shell / install snippets, which
 * the spec explicitly sanctions. Plain code surfaces stay plain code
 * surfaces for JSON and tool output.
 */
interface Line {
  type: 'command' | 'cmd' | 'comment' | 'success' | 'error' | 'warn' | 'muted' | 'output' | 'flag' | 'path';
  text: string;
}
interface Props {
  lines?: Line[];
  title?: string | null;
  prompt?: string;
  cursor?: boolean;
}

const { lines = [], title = null, prompt = 'omnia ❯', cursor = true } = Astro.props as Props;

const LINE: Record<string, { color: string; glyph?: string; gc?: string }> = {
  output: { color: 'var(--star-400)' },
  comment: { color: 'var(--star-950)' },
  muted: { color: 'var(--star-900)' },
  flag: { color: 'var(--starlight-200)' },
  path: { color: 'var(--ion-cyan)' },
  success: { color: 'var(--pulsar-300)', glyph: '✓ ', gc: 'var(--pulsar-500)' },
  error: { color: 'var(--signal-red-300)', glyph: '✗ ', gc: 'var(--signal-red)' },
  warn: { color: 'var(--gold-300)', glyph: '! ', gc: 'var(--amber-500)' },
};

const isCmd = (l: Line) => l.type === 'command' || l.type === 'cmd';
const lastIsCommand = lines.length > 0 && isCmd(lines[lines.length - 1]);
---

<div class="atlas-term">
  {
    title != null && (
      <div class="atlas-term-title">
        <span class="atlas-term-dots">
          <span style="background:var(--star-dim)" /><span style="background:var(--star-dim)" /><span
            style="background:var(--gold-500)"
          />
        </span>
        <span class="atlas-term-name">{title}</span>
      </div>
    )
  }
  <div class="atlas-term-body">
    {
      lines.map((ln, i) => {
        if (isCmd(ln)) {
          return (
            <div class="atlas-term-line atlas-term-cmd">
              <span class="atlas-term-prompt">{prompt}</span>
              <span class="atlas-term-text">
                {ln.text}
                {cursor && i === lines.length - 1 && <span class="atlas-term-cursor" />}
              </span>
            </div>
          );
        }
        const spec = LINE[ln.type] ?? { color: 'var(--star-400)' };
        return (
          <div class="atlas-term-line" style={`color:${spec.color}`}>
            {spec.glyph && <span style={`color:${spec.gc}`}>{spec.glyph}</span>}
            {ln.text}
          </div>
        );
      })
    }
    {
      cursor && !lastIsCommand && (
        <div class="atlas-term-line atlas-term-cmd">
          <span class="atlas-term-prompt">{prompt}</span>
          <span class="atlas-term-cursor" />
        </div>
      )
    }
  </div>
</div>

<style>
  .atlas-term {
    border-radius: var(--radius-lg);
    overflow: hidden;
    border: 1px solid var(--hairline);
    background: var(--ink-void);
    box-shadow: var(--shadow-card);
  }
  .atlas-term-title {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 9px 14px;
    border-bottom: 1px solid var(--hairline);
    background: var(--ink-void);
  }
  .atlas-term-dots {
    display: inline-flex;
    gap: 5px;
  }
  .atlas-term-dots > span {
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }
  .atlas-term-name {
    font: 500 11px/1 var(--font-mono);
    color: var(--star-900);
    margin-left: 4px;
  }
  .atlas-term-body {
    padding: 14px 16px;
    font: 500 12.5px/1.75 var(--font-mono);
    color: var(--star-400);
    overflow: auto;
  }
  .atlas-term-line {
    white-space: pre-wrap;
  }
  .atlas-term-cmd {
    display: flex;
    gap: 8px;
  }
  .atlas-term-prompt {
    color: var(--gold-500);
    user-select: none;
  }
  .atlas-term-text {
    color: var(--star-100);
    flex: 1;
  }
  .atlas-term-cursor {
    display: inline-block;
    width: 8px;
    height: 1.05em;
    margin-left: 3px;
    vertical-align: text-bottom;
    background: var(--gold-500);
    animation: atlas-blink 1.1s steps(1) infinite;
  }
  @media (prefers-reduced-motion: reduce) {
    .atlas-term-cursor {
      animation: none;
    }
  }
</style>
```

- [ ] **Step 2: Verify it compiles**

```bash
npm --prefix docs run build
```

Expected: exits 0. (It renders nothing yet — Task 7 mounts it.)

- [ ] **Step 3: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/src/components/Terminal.astro
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): add the Atlas Terminal component for shell snippets"
```

---

### Task 6: Header lockup and the hero override

Two things Starlight cannot express in frontmatter: the eyebrow above the H1, and the chart panel beside it. Both need a `Hero` component override. The site already overrides `PageFrame` and `Header`, so this follows an established pattern.

**Files:**
- Create: `docs/public/logo-omnia.svg` (copied), `docs/public/star-glyph-gold.svg` (copied)
- Create: `docs/src/components/Hero.astro`
- Modify: `docs/astro.config.mjs` (`logo.src`, `components.Hero`)
- Delete: `docs/public/logo.svg`, `docs/public/hero-architecture.svg`

**Interfaces:**
- Consumes: `ConstellationGraph` (Task 4).
- Produces: the authoritative home-page hero.

- [ ] **Step 1: Copy the Omnia mark**

```bash
cp "$BUNDLE"/assets/logo-omnia.svg docs/public/logo-omnia.svg
```

Do **not** copy `star-glyph-gold.svg` yet. The spec lists it for the "footer / family-bar lockup", but the family bar comes from `@altairalabs/brand` and already ships its own lockup, and `docs/src/components/AltairaFooter.astro` already hand-inlines several SVGs including a star (around line 113). Inspect the footer first:

```bash
grep -n "viewBox" docs/src/components/AltairaFooter.astro
```

If the existing star is visually equivalent to the bundle's, leave the footer alone and skip the asset entirely. Only if the footer has no star, or its star is off-brand, copy it in and wire it — and say so at review. A copied-but-unreferenced file in `public/` is dead weight that ships to the CDN.

- [ ] **Step 2: Point the header at the Omnia mark**

In `docs/astro.config.mjs`, replace the `logo` block:

```js
      logo: {
        src: './public/logo-omnia.svg',
        alt: 'Omnia',
      },
```

The spec is explicit that Codegen Sandbox gets **no logo of its own** — it is a component of Omnia. `title: 'Codegen Sandbox'` stays and serves as the wordmark.

- [ ] **Step 3: Register the Hero override**

In the `components` block of `docs/astro.config.mjs`, add alongside the existing entries:

```js
        // The splash hero carries an eyebrow and a ConstellationGraph panel,
        // neither of which Starlight's hero frontmatter can express.
        Hero: './src/components/Hero.astro',
```

- [ ] **Step 4: Write `docs/src/components/Hero.astro`**

```astro
---
import ConstellationGraph from './ConstellationGraph.astro';

/**
 * Splash hero for the home page. Overrides Starlight's default Hero so the
 * page can carry the Atlas eyebrow and the brain/hands star chart.
 *
 * The tagline and actions still come from index.mdx so the copy stays with
 * the content. The H1 and eyebrow are design-owned and live here.
 *
 * Starlight moved route data from Astro.props to Astro.locals.starlightRoute
 * in 0.32. The sibling overrides in this repo (Header.astro,
 * SitePageFrame.astro) still use the Astro.props form, so read both and
 * prefer whichever is populated.
 */
const route = (Astro.locals as any).starlightRoute ?? (Astro.props as any);
const hero = route?.entry?.data?.hero ?? {};
const tagline = hero.tagline ?? '';
const actions = hero.actions ?? [];

if (!tagline) {
  // Fail loudly rather than shipping a hero with a missing tagline.
  throw new Error(
    'Hero.astro: could not read hero frontmatter. Check whether this Starlight ' +
      'version exposes route data on Astro.locals.starlightRoute or Astro.props.'
  );
}

const heroNodes = [
  { id: 'brain', x: 46, y: 120, kind: 'entry' as const, label: 'agent · brain' },
  { id: 'wire', x: 160, y: 120, kind: 'branch' as const, label: 'mcp wire' },
  { id: 'fs', x: 290, y: 44, kind: 'tool' as const, label: 'filesystem' },
  { id: 'sh', x: 290, y: 120, kind: 'tool' as const, label: 'shell' },
  { id: 'vf', x: 290, y: 196, kind: 'tool' as const, label: 'verification' },
  { id: 'vol', x: 424, y: 120, kind: 'output' as const, label: 'workspace' },
];
const heroEdges = [
  { from: 'brain', to: 'wire' },
  { from: 'wire', to: 'fs' },
  { from: 'wire', to: 'sh' },
  { from: 'wire', to: 'vf' },
  { from: 'fs', to: 'vol' },
  { from: 'sh', to: 'vol' },
  { from: 'vf', to: 'vol' },
];
---

<div class="cs-hero">
  <div class="cs-hero-inner">
    <div class="cs-hero-copy">
      <p class="cs-eyebrow">MCP provider · promptkit codegen</p>
      <h1>The agent thinks.<br />The sandbox has the hands.</h1>
      <p class="cs-tagline">{tagline}</p>
      <div class="cs-actions">
        {
          actions.map((a: { text: string; link: string; variant?: string }) => (
            <a href={a.link} class={`cs-action ${a.variant === 'primary' ? 'primary' : 'minimal'}`}>
              {a.text}
            </a>
          ))
        }
      </div>
    </div>

    <div class="cs-hero-panel">
      <p class="cs-panel-caption">brain / hands · one mcp wire</p>
      <ConstellationGraph
        nodes={heroNodes}
        edges={heroEdges}
        width={470}
        height={240}
        showLabels
        title="A PromptKit agent sends tool calls over an MCP wire to filesystem, shell and verification tools, which act on a shared workspace."
      />
    </div>
  </div>
</div>

<style>
  /* Full-bleed band. Starlight pads the content column, so the hero pulls
     back out to the viewport edge. --sl-content-pad-x is Starlight's own
     token; the fallback keeps this sane if the name ever changes. Verify in
     DevTools that the band reaches both edges with no horizontal scrollbar —
     if it overflows, drop the margin-inline line and let the band sit inside
     the content column instead. */
  .cs-hero {
    background: var(--atmo-starlight);
    border-bottom: 1px solid var(--hairline);
    padding: 76px 48px 64px;
    margin-inline: calc(-1 * var(--sl-content-pad-x, 1.5rem));
  }
  .cs-hero-inner {
    max-width: 1080px;
    margin: 0 auto;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
    gap: 48px;
    align-items: center;
  }
  .cs-eyebrow {
    font: 500 11px/1 var(--font-mono);
    letter-spacing: var(--tracking-eyebrow);
    text-transform: uppercase;
    color: var(--text-faint);
    margin: 0 0 20px;
  }
  .cs-hero-copy h1 {
    margin: 0 0 20px;
    font: 600 46px/1.06 var(--font-sans);
    letter-spacing: var(--tracking-hero);
    color: var(--text-heading);
    text-wrap: pretty;
  }
  .cs-tagline {
    margin: 0 0 32px;
    max-width: 44ch;
    font: 400 20px/1.55 var(--font-sans);
    color: var(--text-muted);
    text-wrap: pretty;
  }
  .cs-actions {
    display: flex;
    gap: 12px;
    align-items: center;
    flex-wrap: wrap;
  }
  .cs-action {
    display: inline-flex;
    align-items: center;
    padding: 0 20px;
    height: 44px;
    border-radius: var(--radius-md);
    font: 500 15px/1 var(--font-sans);
    text-decoration: none;
  }
  /* The one gold element on the page. */
  .cs-action.primary {
    background: var(--gold-500);
    color: var(--text-on-gold);
    border: 1px solid var(--gold-500);
    box-shadow: var(--glow-gold);
  }
  .cs-action.minimal {
    border: 1px solid var(--hairline-strong);
    color: var(--text-body);
    background: transparent;
  }
  .cs-action.minimal:hover {
    border-color: var(--border-strong);
    color: var(--text-heading);
  }
  .cs-hero-panel {
    border: 1px solid var(--hairline);
    border-radius: var(--radius-3xl);
    background: var(--surface-1);
    padding: 18px 16px;
  }
  .cs-panel-caption {
    font: 500 11px/1 var(--font-mono);
    letter-spacing: var(--tracking-label);
    text-transform: uppercase;
    color: var(--text-faint);
    padding: 2px 6px 14px;
    margin: 0;
  }
</style>
```

- [ ] **Step 5: Retire the old assets**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox rm docs/public/logo.svg docs/public/hero-architecture.svg
```

Then remove the now-dangling `hero.image` block from `docs/src/content/docs/index.mdx` frontmatter (lines 6–7) — the whole `image:` key and its `html:` child. Leave `tagline` and `actions` in place; the Hero override reads them.

- [ ] **Step 6: Build and verify**

```bash
npm --prefix docs run build
grep -c "cs-eyebrow" docs/dist/index.html
grep -c "hero-architecture" docs/dist/index.html || echo "old illustration gone"
```

Expected: `cs-eyebrow` appears once; `hero-architecture` appears zero times.

- [ ] **Step 7: Visual check**

```bash
npm --prefix docs run dev
```

At `http://localhost:4321/`, confirm: eyebrow in faint uppercase mono (never gold); a two-line H1; the star chart to the right with gold twinkling entry and output nodes; the `Getting Started` button gold with a glow; `Architecture` a hairline outline. Then toggle to dark and confirm the chart and hero atmosphere re-skin.

- [ ] **Step 8: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/public docs/src/components/Hero.astro docs/astro.config.mjs docs/src/content/docs/index.mdx
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): Omnia header lockup and Atlas splash hero with star chart"
```

---

### Task 7: Home-page content restructure

**Files:**
- Modify: `docs/src/content/docs/index.mdx`

**Interfaces:**
- Consumes: `Terminal` (Task 5).

- [ ] **Step 1: Remove the "How it fits" Mermaid block**

In `docs/src/content/docs/index.mdx`, delete the fenced ```mermaid block under `## How it fits` (currently lines 52–62). **Keep** the `## How it fits` heading and its prose paragraph — the hero star chart now carries what the diagram showed.

- [ ] **Step 2: Keep "Defence in depth" exactly as it is**

Do not touch the `## Defence in depth` heading or its Mermaid block. The spec relocates it to Architecture; we are deliberately leaving it here to see how it reads. Task 8 restyles it.

- [ ] **Step 3: Add the "Pull it, run it" section**

Insert after the `## Tool surface` `<CardGrid>` closing tag and before `## How it fits`:

```mdx
## Pull it, run it

One container, one flag, one workspace mount. The sandbox is a provider behind PromptKit's agent — PromptKit asks for a sandbox, the provider returns an MCP endpoint.

<Terminal
  title="codegen-sandbox · local"
  prompt="❯"
  lines={[
    { type: 'command', text: 'docker run -p 8080:8080 -v $PWD:/workspace ghcr.io/altairalabs/codegen-sandbox' },
    { type: 'comment', text: '# mcp over http+sse · workspace mounted read-write' },
    { type: 'success', text: 'codegen-sandbox listening on :8080 (workspace=/workspace)' },
    { type: 'muted', text: '  32 tools registered · scrubbing on · denylist on' },
  ]}
/>
```

- [ ] **Step 4: Add the import**

Extend the existing import line (currently line 19) so both components come in together:

```mdx
import { Card, CardGrid } from '@astrojs/starlight/components';
import Terminal from '../../components/Terminal.astro';
```

- [ ] **Step 5: Replace the plain "Next" link with the design's rule row**

Replace the final line `[Next: Getting Started →](/getting-started/)` with:

```mdx
<div class="cs-next">
  <span>next · getting started</span>
  <a href="/getting-started/">Build, run and talk to the sandbox →</a>
</div>
```

Then append the rule to `docs/src/styles/custom.css`:

```css
/* Home-page "next" footer row — hairline rule, mono label left, link right. */
.cs-next {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding-top: 32px;
  margin-top: 64px;
  border-top: 1px solid var(--hairline);
}
.cs-next > span {
  font: 400 13px/1.5 var(--font-mono);
  color: var(--text-faint);
}
.cs-next > a {
  font: 500 15px/1 var(--font-sans);
}
```

- [ ] **Step 6: Build and verify the block counts**

```bash
npm --prefix docs run build
grep -c "atlas-term" docs/dist/index.html
grep -c "mermaid" docs/src/content/docs/index.mdx
```

Expected: the terminal renders (non-zero), and `index.mdx` now contains exactly **one** ```mermaid fence (Defence in depth) where it previously had two.

- [ ] **Step 7: Link check**

```bash
npm --prefix docs run check-links
```

Expected: no broken links. The `/getting-started/` link moved into raw HTML, so this confirms it still resolves.

- [ ] **Step 8: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/src/content/docs/index.mdx docs/src/styles/custom.css
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): restructure the home page onto the Atlas design"
```

---

### Task 8: Re-theme Mermaid to Atlas

Mermaid stays (three blocks now: one on the home page, two on Architecture), so it must stop looking like the old blue/cyan Galaxy theme. The file is already centrally themed and theme-aware, so this is a contained change. Note it already hardcodes `#3b82f6`, which happens to be Atlas `--starlight-500` — the values largely survive; what changes is that they are read from tokens, and the hand-drawn look goes.

**Files:**
- Modify: `docs/public/mermaid-init.js:3-44`

- [ ] **Step 1: Confirm the current look setting**

```bash
grep -n "handDrawn\|3b82f6" docs/public/mermaid-init.js | head
```

Expected: `look: 'handDrawn'` on line 9 and several `#3b82f6` literals.

- [ ] **Step 2: Replace `getThemeConfig()` (lines 3–44) with a token-reading version**

```js
function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

function getThemeConfig() {
  // Read straight from the Atlas tokens so the diagrams re-skin with the
  // rest of the page when data-theme flips. Fallbacks are the Atlas
  // light-sky values, used only if the token sheet has not parsed yet.
  const line = cssVar('--starlight-500', '#3b82f6');
  const textColor = cssVar('--text-body', '#1e293b');
  const labelColor = cssVar('--text-faint', '#5E708C');

  return {
    startOnLoad: false,
    // 'handDrawn' fights the instrument-grade Atlas register.
    look: 'classic',
    theme: 'base',
    themeVariables: {
      // No fills — transparent backgrounds over the Atlas canvas.
      primaryColor: 'transparent',
      secondaryColor: 'transparent',
      tertiaryColor: 'transparent',
      // Starlight-blue borders, matching ConstellationGraph edges.
      primaryBorderColor: line,
      secondaryBorderColor: line,
      tertiaryBorderColor: line,
      primaryTextColor: textColor,
      secondaryTextColor: textColor,
      tertiaryTextColor: textColor,
      lineColor: line,
      edgeLabelBackground: 'transparent',
      labelBackground: 'transparent',
      labelTextColor: labelColor,
      background: 'transparent',
      mainBkg: 'transparent',
      nodeBkg: 'transparent',
      git0: line,
      git1: line,
      gitBranchLabel0: line,
      gitBranchLabel1: line,
      commitLabelBackground: 'transparent',
      fontFamily: cssVar('--font-sans', 'system-ui, -apple-system, sans-serif'),
    },
  };
}
```

Leave the rest of the file — `renderMermaid()`, the `astro:page-load` listener and the `MutationObserver` — unchanged. The observer already re-initialises on `data-theme` change, which is what makes the token re-read work.

- [ ] **Step 3: Build and visually verify all three diagrams**

```bash
npm --prefix docs run build
npm --prefix docs run dev
```

Check `http://localhost:4321/` (Defence in depth) and `http://localhost:4321/architecture/` (two flowcharts). In both themes confirm: no hand-drawn wobble, edges and borders match the hero star chart's blue, node labels are readable, backgrounds are transparent over the Atlas canvas.

- [ ] **Step 4: Commit**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox add docs/public/mermaid-init.js
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox commit -s -m "feat(docs): theme Mermaid diagrams from Atlas tokens"
```

---

### Task 9: Galaxy audit and full-site verification

**Files:**
- Modify: `docs/src/styles/custom.css` (only if dead `--fb-*` rules are found)

- [ ] **Step 1: Check whether the Galaxy theme still contributes anything**

```bash
grep -rn -- "--fb-" docs/src/styles/custom.css
```

The Atlas mapping supersedes Galaxy's accent pin. The spec (item 9) says to keep `starlight-theme-galaxy` only if you still want its layout extras. **Decision for this pass: keep the plugin** — removing it is a separate change with its own blast radius. Leave the `--fb-*` lines that the drop-in already carries; they cost nothing and keep Galaxy's TOC/steps consistent.

- [ ] **Step 2: Full build and link check**

```bash
npm --prefix docs run build
npm --prefix docs run check-links
```

Expected: build exits 0, zero broken links.

- [ ] **Step 3: Sweep one page of every kind in both themes**

```bash
npm --prefix docs run dev
```

Visit each and check in **both** light and dark:

| URL | What to confirm |
|---|---|
| `/` | hero, star chart, tool cards, terminal, Mermaid, next row |
| `/getting-started/` | prose, code fences, TOC rail, sidebar active state |
| `/architecture/` | both Mermaid diagrams, tables |
| `/tools/read/` | schema table in mono, `yes` green, Related list |
| `/tools/ast-edits/` | multi-tool page — the table-heaviest layout |
| `/concepts/language-support/` | the widest table on the site; must not overflow horizontally |
| `/operations/docker/` | long page, many code fences |

Confirm on every one: the family bar renders above the header and takes no gold; the sidebar group labels are uppercase mono; no element is set in a system font.

- [ ] **Step 4: Confirm the gold budget**

On `/`, count the gold elements. Expected: the primary CTA, plus the star chart's entry/output nodes and the terminal prompt/cursor. Per the Global Constraints, **do not deviate** — report the count for review.

- [ ] **Step 5: Commit any fixes and push the branch**

```bash
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox status
git -C /Users/chaholl/repos/altairalabs/codegen-sandbox push -u origin feat/docs-atlas-restyle
```

Do **not** open a PR. Per the working agreement, iterative edits batch onto this branch until the work is called done.

---

## Open questions for review

Raise these when the branch is first looked at — none of them block implementation:

1. **Gold budget.** The design rule says gold once per view; the specified components put gold in three places on the home page (CTA, chart entry/output stars, terminal prompt). Implemented per component spec — confirm that is intended.
2. **`fonts.css` was broken in the delivered bundle** (eight static faces, five variable files, no overlap in filenames). Task 1 rewrites it. Worth reporting upstream so the next handoff ships a correct one.
3. **Defence in depth on the home page.** Kept deliberately, against the spec. Decide after seeing it whether it stays or moves to Architecture.
4. **Galaxy theme.** Kept for now (Task 9). Removing it and its `--fb-*` variables is a clean follow-up if its layout extras turn out to be unused.
