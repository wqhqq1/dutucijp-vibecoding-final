import {defineConfig} from 'vite';
import {mockApi} from './dev/mock-api.ts';

// The production build (default mode) uses no plugins, matching the previous
// zero-config build. `--mode mock` adds the clearly labelled sample-data preview
// on its own ports so it never collides with the real :8080 service.
export default defineConfig(({mode})=>mode==='mock'?{
  plugins:[mockApi()],
  server:{host:'127.0.0.1',port:5174,strictPort:true},
  preview:{host:'127.0.0.1',port:4174,strictPort:true},
}:{});
