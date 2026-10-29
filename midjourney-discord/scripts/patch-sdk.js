// Patch the published SDK's stale command cache. No human-verification bypass.
import { readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
const require = createRequire(import.meta.url)
const path = join(dirname(require.resolve('midjourney')), 'command.js')
let source = readFileSync(path, 'utf8')
const start = source.indexOf('    async cacheCommand(')
const end = source.indexOf('    async allCommand(', start)
if (start < 0 || end < 0) throw new Error('Unsupported midjourney SDK patch target')
source = source.slice(0, start) + `    async cacheCommand(name) {
 if (!this.config.ServerId) throw new Error('Discord server ID required');
 if (!this.cache[name]) await this.allCommand();
 if (!this.cache[name]) throw new Error('Midjourney command unavailable: ' + name);
 return this.cache[name];
 }
` + source.slice(end)
source = source.replace('if (name) {', 'if (name && command.application_id === this.config.BotId) {')
writeFileSync(path, source)
// Treat human challenges and moderation acknowledgements as manual action.
const wsPath = join(dirname(require.resolve('midjourney')), 'discord.ws.js')
let ws = readFileSync(wsPath, 'utf8')
ws = ws.replaceAll('return this.continue(message);', "return this.EventError(id, new Error('Discord account requires manual attention'));")
ws = ws.replace('await this.verifyHuman(message);', "this.EventError(id, new Error('Discord account requires manual human verification'));")
writeFileSync(wsPath, ws)
