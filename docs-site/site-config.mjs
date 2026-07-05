// Shared between astro.config.mjs and scripts/sync-docs.mjs, which can't
// import astro.config.mjs directly (it transitively pulls in Starlight,
// which plain Node can't load outside Astro's own build tooling).
export const site = "https://cpcf.github.io";
export const base = "/gess";
