import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react-swc";
import { VitePWA } from "vite-plugin-pwa";
import { defineConfig } from "vite";
import { resolve } from 'path'

const umamiUrl = process.env.UMAMI_URL?.trim() ?? "";
const umamiWebsiteId = process.env.UMAMI_WEBSITE_ID?.trim() ?? "";
const viewerServerUrl = process.env.SERVER_URL?.trim() ?? "";
const viewerUsername = process.env.VIEWER_USERNAME?.trim() ?? "";
const viewerPassword = process.env.VIEWER_PASSWORD ?? "";

export default defineConfig({
  envPrefix: ["VITE_", "UMAMI_"],
  define: {
    "import.meta.env.UMAMI_URL": JSON.stringify(umamiUrl),
    "import.meta.env.UMAMI_WEBSITE_ID": JSON.stringify(umamiWebsiteId),
    "import.meta.env.SERVER_URL": JSON.stringify(viewerServerUrl),
    "import.meta.env.VIEWER_USERNAME": JSON.stringify(viewerUsername),
    "import.meta.env.VIEWER_PASSWORD": JSON.stringify(viewerPassword),
  },
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      strategies: 'injectManifest',
      srcDir: 'src',
      filename: 'sw.ts',
      registerType: 'prompt',
      scope: '/',
      injectRegister: false,
      manifest: {
        name: 'Bilirec',
        short_name: 'Bilirec',
        description: 'Bilibili live recording manager',
        start_url: '/',
        display: 'standalone',
        background_color: '#f2f4f6',
        theme_color: '#7db3e8',
        icons: [
          {
            src: '/icon-192.svg',
            sizes: '192x192',
            type: 'image/svg+xml',
            purpose: 'any maskable',
          },
          {
            src: '/icon-512.svg',
            sizes: '512x512',
            type: 'image/svg+xml',
            purpose: 'any maskable',
          },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,ico,woff2}'],
      },
      devOptions: {
        enabled: false,
      },
    }),
  ],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src')
    }
  },
  server: {
    port: 3000,
    host: true,
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
});
