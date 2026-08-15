// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
  site: 'https://codegen-sandbox.altairalabs.ai',
  integrations: [
    starlight({
      title: 'Codegen Sandbox',
      description:
        'A Docker-based MCP server that ships safe codegen tools (Read, Edit, Write, Glob, Grep, Bash, run_tests, run_lint, run_typecheck) for PromptKit agents. Hook up vendor MCP servers alongside for web search / fetch.',
      // Codegen Sandbox is a component of Omnia and gets no logo of its own:
      // the header is the Omnia Star Tile plus the title as a wordmark.
      logo: {
        src: './public/atlas/logo-omnia.svg',
        alt: 'Omnia',
        replacesTitle: false,
      },
      components: {
        PageFrame: './src/components/SitePageFrame.astro',
        // Adds the AltairaLabs masterbrand family bar as a strip across the
        // top of the fixed header. Paired with the --fam-h rules in
        // src/styles/custom.css, which reserve its height.
        Header: './src/components/Header.astro',
        // Makes light the default sky. Starlight has no defaultTheme option;
        // see the component for what differs from the stock provider.
        ThemeProvider: './src/components/ThemeProvider.astro',
        // The splash hero carries an eyebrow and a ConstellationGraph panel,
        // neither of which Starlight's hero frontmatter can express.
        Hero: './src/components/Hero.astro',
      },
      // No starlight-theme-galaxy: the Atlas mapping supersedes its accent
      // pin, omnia doesn't use it and promptkit never registers it, and it
      // shipped ~13 woff2 of Inter + JetBrains Mono this site never renders
      // plus a gradient rule under every H2 that Atlas does not specify.
      customCss: ['@altairalabs/brand/family-bar-starlight.css', './src/styles/custom.css'],
      // Code blocks: inky Atlas surfaces + starlight-leaning syntax
      // (poimandres for the night sky, a light theme for the printed chart).
      // Mirrors omnia's block — Codegen Sandbox takes the Omnia register.
      expressiveCode: {
        themes: ['poimandres', 'github-light'],
        styleOverrides: {
          borderColor: 'var(--hairline)',
          borderRadius: 'var(--radius-lg)',
          codeBackground: 'var(--surface-code)',
          codeFontFamily: 'var(--font-mono)',
          codeFontSize: '13.5px',
          uiFontFamily: 'var(--font-sans)',
          frames: {
            editorActiveTabBackground: 'var(--surface-2)',
            editorActiveTabIndicatorBottomColor: 'var(--accent-inter)',
            editorTabBarBackground: 'var(--ink-void)',
            editorBackground: 'var(--surface-code)',
            terminalBackground: 'var(--surface-code)',
            terminalTitlebarBackground: 'var(--ink-void)',
          },
        },
      },
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/AltairaLabs/codegen-sandbox',
        },
      ],
      sidebar: [
        {
          label: 'Overview',
          items: [
            { label: 'Introduction', link: '/' },
            { label: 'Getting Started', link: '/getting-started/' },
            { label: 'Architecture', link: '/architecture/' },
          ],
        },
        {
          label: 'Tools',
          collapsed: false,
          autogenerate: { directory: 'tools' },
        },
        {
          label: 'Guides',
          collapsed: true,
          autogenerate: { directory: 'guides' },
        },
        {
          label: 'Concepts',
          collapsed: true,
          autogenerate: { directory: 'concepts' },
        },
        {
          label: 'Reference',
          collapsed: true,
          autogenerate: { directory: 'reference' },
        },
        {
          label: 'Operations',
          collapsed: true,
          autogenerate: { directory: 'operations' },
        },
      ],
      head: [
        {
          tag: 'script',
          attrs: {
            type: 'module',
            src: '/mermaid-init.js',
          },
        },
      ],
    }),
  ],
});
