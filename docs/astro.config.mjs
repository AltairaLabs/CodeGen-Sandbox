// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeGalaxy from 'starlight-theme-galaxy';

// https://astro.build/config
export default defineConfig({
  site: 'https://codegen-sandbox.altairalabs.ai',
  integrations: [
    starlight({
      title: 'Codegen Sandbox',
      description:
        'A Docker-based MCP server that ships safe codegen tools (Read, Edit, Write, Glob, Grep, Bash, run_tests, run_lint, run_typecheck) for PromptKit agents. Hook up vendor MCP servers alongside for web search / fetch.',
      logo: {
        src: './public/logo.svg',
        alt: 'Codegen Sandbox',
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
      },
      plugins: [starlightThemeGalaxy()],
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
