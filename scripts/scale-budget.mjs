// Connection-budget gate; accepts numbers only and never reads credentials.
import {pathToFileURL} from 'node:url';
export function budget({maxConnections, reserve, otherConnections, replicas, surge, pool, readPool = 0}) {
  const values = {maxConnections, reserve, otherConnections, replicas, surge, pool, readPool};
  for (const [key, value] of Object.entries(values)) {
    if (!Number.isSafeInteger(value) || value < 0) throw Error(`${key} must be a non-negative integer`);
  }
  if (replicas < 1 || pool < 2 || (readPool > 0 && readPool < 2)) throw Error('At least one replica and pools of at least two are required');
  const required = (replicas + surge) * (pool + readPool) + otherConnections + reserve;
  return {required, maxConnections, permitted: required <= maxConnections};
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const args = process.argv.slice(2).map(Number);
    if (args.length !== 7) throw Error('Usage: node scripts/scale-budget.mjs MAX_CONNECTIONS RESERVED OTHER_CONNECTIONS REPLICAS SURGE POOL READ_POOL_ON_SAME_DB');
    const [maxConnections, reserve, otherConnections, replicas, surge, pool, readPool] = args;
    const result = budget({maxConnections, reserve, otherConnections, replicas, surge, pool, readPool});
    console.log(JSON.stringify(result));
    process.exitCode = result.permitted ? 0 : 1;
  } catch (error) { console.error(error.message); process.exitCode = 2; }
}
