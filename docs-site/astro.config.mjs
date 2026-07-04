// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

const repo = 'https://github.com/cpcf/gess';

// https://astro.build/config
export default defineConfig({
	integrations: [
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
				{ label: 'Developer guide', slug: 'contributing' },
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
