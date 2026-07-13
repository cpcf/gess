// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import mermaid from 'astro-mermaid';
import { site, base } from './site-config.mjs';

const repo = 'https://github.com/cpcf/gess';

// https://astro.build/config
export default defineConfig({
	// GitHub Pages serves a project repo at <user>.github.io/<repo>, so
	// internal links and asset URLs need the /gess base path baked in.
	site,
	base,
	integrations: [
		mermaid({
			theme: 'neutral',
			autoTheme: true,
		}),
		starlight({
			title: 'Gess',
			description:
				'A Go rules engine with a Rete-based runtime and .gess compiler',
			social: [{ icon: 'github', label: 'GitHub', href: repo }],
			sidebar: [
				{ label: 'Tutorial', slug: 'tutorial' },
				{ label: 'Core concepts', slug: 'concepts' },
				{ label: '.gess language reference', slug: 'gess-language' },
				{ label: 'Go API guide', slug: 'go-api' },
				{ label: 'Value JSON', slug: 'value-json' },
				{ label: 'Explain JSON', slug: 'explain-json' },
				{ label: 'Session lifecycle', slug: 'session-lifecycle' },
				{ label: 'Command-line tools', slug: 'cli' },
				{ label: 'Advanced behavior', slug: 'advanced' },
				{ label: 'Examples map', slug: 'examples' },
				{ label: 'Interactive tutorial workshop', slug: 'tutorial-workshop' },
				{ label: 'Developer guide', slug: 'contributing' },
				{
					label: 'API reference',
					items: [
						{ label: 'rules', slug: 'reference/rules' },
						{ label: 'session', slug: 'reference/session' },
						{ label: 'dsl', slug: 'reference/dsl' },
						{ label: 'scenario', slug: 'reference/scenario' },
					],
				},
			],
		}),
	],
	markdown: {
		shikiConfig: {
			langAlias: {
				cl: 'common-lisp',
			},
		},
	},
});
