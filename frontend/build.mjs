import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = path.dirname(fileURLToPath(import.meta.url));
await build({
  entryPoints: [path.join(root, 'src/App.jsx')],
  outfile: path.join(root, '../web/app.js'),
  bundle: true,
  minify: true,
  jsx: 'automatic',
  target: ['es2022'],
  define: { 'process.env.NODE_ENV': '"production"' },
  legalComments: 'eof',
});
