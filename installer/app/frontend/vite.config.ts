import {defineConfig, type Plugin} from 'vite'
import react from '@vitejs/plugin-react'
import {mkdirSync, writeFileSync} from 'node:fs'
import {join} from 'node:path'

// The Go window embeds frontend/dist, so the folder must hold at least one file on a fresh
// checkout or the Go build fails ("no matching files found"). The repo tracks an empty
// dist/.gitkeep for that, but every Vite build empties dist and would delete it, which
// then shows up as a deletion in the next commit. Put it back after each build.
function keepDistPlaceholder(): Plugin {
  let outDir = 'dist'
  return {
    name: 'keep-dist-placeholder',
    apply: 'build',
    configResolved(config) {
      outDir = config.build.outDir
    },
    closeBundle() {
      mkdirSync(outDir, {recursive: true})
      writeFileSync(join(outDir, '.gitkeep'), '')
    },
  }
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), keepDistPlaceholder()]
})
