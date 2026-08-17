import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'

// https://vitepress.dev/reference/site-config
export default withMermaid(
  defineConfig({
    title: 'Syrinx',
    description: 'A distributed, P2P-ish content platform',
    // Project Pages: https://var0xyz.github.io/syrinx/
    base: '/syrinx/',
    cleanUrls: true,
    lastUpdated: true,
    themeConfig: {
      nav: [
        { text: 'Home', link: '/' },
        { text: 'Architecture', link: '/architecture' },
        { text: 'Operators', link: '/operators' },
        {
          text: 'GitHub',
          link: 'https://github.com/var0xyz/syrinx'
        }
      ],
      sidebar: [
        {
          text: 'Introduction',
          items: [
            { text: 'What is Syrinx?', link: '/' },
            { text: 'Philosophy', link: '/philosophy' },
            { text: 'Planned features', link: '/planned' }
          ]
        },
        {
          text: 'How it works',
          items: [
            { text: 'Architecture', link: '/architecture' },
            { text: 'Trust model', link: '/trust' },
            { text: 'Cryptography', link: '/cryptography' },
            { text: 'Content distribution', link: '/content' },
            { text: 'Relay model', link: '/relay-model' },
            { text: 'Invites', link: '/invites' },
            { text: 'Identity, invites & recovery', link: '/identity' },
            { text: 'Deletion', link: '/deletion' }
          ]
        },
        {
          text: 'Guides',
          items: [
            { text: 'Operators', link: '/operators' },
            { text: 'Contributors', link: '/contributors' }
          ]
        }
      ],
      socialLinks: [
        { icon: 'github', link: 'https://github.com/var0xyz/syrinx' }
      ],
      search: {
        provider: 'local'
      },
      outline: {
        level: [2, 3]
      }
    }
  })
)
