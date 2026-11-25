import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The build lands inside the Go package that embeds it, so `go build` alone
// produces the whole panel. There is no separate asset directory to deploy.
export default defineConfig({
    plugins: [react()],
    build: {
        outDir: '../internal/web/dist',
        emptyOutDir: true,
        // Names carry a content hash, which is what lets the Go side cache
        // them forever and never serve a stale release.
        assetsDir: 'assets',
        sourcemap: false,
    },
    server: {
        port: 5173,
        // During development Vite serves the UI and forwards the API to the
        // Go panel, so the browser sees one origin and cookies work.
        proxy: {
            '/api': {
                target: 'http://127.0.0.1:8080',
                changeOrigin: false,
            },
        },
    },
});
