// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import mermaid from 'astro-mermaid';

const repo = 'https://github.com/cpcf/gess';

// https://astro.build/config
export default defineConfig({
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
