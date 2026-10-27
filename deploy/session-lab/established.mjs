// Pure helpers: warmup metadata must never silently select fewer identities.
export function establishedMode(value) {
 if(!['live','established'].includes(value||'live'))throw Error('Unknown authentication mode');
 return value==='established';
}
export function bindEstablished(actor, authenticated, expectedCount) {
 if(!Array.isArray(authenticated)||authenticated.length!==expectedCount)throw Error('Complete authenticated population required');
 const match=authenticated.find(item=>item.id===actor.id);
 if(!match?.token||authenticated.filter(item=>item.id===actor.id).length!==1)throw Error('Authenticated identity binding mismatch');
 return {...actor,token:match.token};
}
