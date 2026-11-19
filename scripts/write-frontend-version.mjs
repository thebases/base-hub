import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'

/**
 * Writes the frontend build version manifest consumed by the SPA and deployment tooling.
 * Input is a single CLI argument pointing to the JSON output file; output is a prettified JSON
 * file with the current BUILD_VERSION value. The script creates the target directory when needed
 * and fails fast when the caller does not provide a destination path.
 */
async function main() {
  const outputArg = process.argv[2]
  if (!outputArg) {
    throw new Error('Usage: node write-frontend-version.mjs <output-path>')
  }

  const resolvedOutputPath = path.resolve(process.cwd(), outputArg)
  const version = process.env.BUILD_VERSION ?? `base_${Date.now()}`
  const payload = `${JSON.stringify({ version }, null, 2)}\n`

  await mkdir(path.dirname(resolvedOutputPath), { recursive: true })
  await writeFile(resolvedOutputPath, payload, 'utf8')
}

await main()
