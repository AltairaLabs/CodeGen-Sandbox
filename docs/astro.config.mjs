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
      plugins: [starlightThemeGalaxy()],
      customCss: ['./src/styles/custom.css'],
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
    }),
  ],
});
