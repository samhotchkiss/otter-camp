import { defineConfig } from 'astro/config';
import mdx from '@astrojs/mdx';
import sitemap from '@astrojs/sitemap';

// https://astro.build/config
export default defineConfig({
  site: 'https://sam.blog',
  output: 'static',
  integrations: [mdx(), sitemap()],
  image: {
    // Astro's built-in image optimization — sharp is the default service
    // for static builds. Images in src/assets/ are automatically optimized.
  },
  markdown: {
    shikiConfig: {
      theme: 'github-dark',
    },
  },
});