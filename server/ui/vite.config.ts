import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The dashboard is embedded into the Go binary via embed.FS from
// server/internal/web/dist — vite is the only writer of that directory.
export default defineConfig({
  plugins: [react()],
  base: '/',
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    // Local dev convenience: proxy the admin API to a locally running server.
    proxy: {
      '/admin': 'http://localhost:8080',
    },
  },
});
