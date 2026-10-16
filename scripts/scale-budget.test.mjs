import test from 'node:test';
import assert from 'node:assert/strict';
import {budget} from './scale-budget.mjs';
const base = {maxConnections: 100, reserve: 20, otherConnections: 30, replicas: 2, surge: 1, pool: 8};
test('accounts for deployment surge and shared workers', () => {
  assert.deepEqual(budget(base), {required: 74, maxConnections: 100, permitted: true});
  assert.equal(budget({...base, replicas: 6}).permitted, false);
});
test('separate read pools against the same database also consume connections', () => {
  assert.equal(budget({...base, readPool: 10}).permitted, false);
});
test('invalid or missing inputs cannot silently pass preflight', () => {
  for (const value of [-1, NaN, Infinity, 2.5, undefined]) assert.throws(() => budget({...base, surge: value}));
  assert.throws(() => budget({...base, pool: 1}));
});
