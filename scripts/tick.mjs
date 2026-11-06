// Tick PROGRESS.md items by unique substring: node scripts/tick.mjs "substr" ...
import fs from 'node:fs';
let s = fs.readFileSync('PROGRESS.md', 'utf8');
for (const needle of process.argv.slice(2)) {
  const lines = s.split('\n');
  const hits = lines.filter(l => l.startsWith('- [ ] ') && l.includes(needle));
  if (hits.length !== 1) { console.error(`"${needle}" matched ${hits.length} open items`); process.exit(1); }
  s = s.replace(hits[0], hits[0].replace('- [ ] ', '- [x] '));
}
fs.writeFileSync('PROGRESS.md', s);
