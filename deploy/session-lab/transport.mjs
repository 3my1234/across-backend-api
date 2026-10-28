// Synthetic clustered coordinates, not real user/device locations.
export function transportPosition(slot,sequence=0){
 if(!Number.isInteger(slot)||slot<1||!Number.isInteger(sequence)||sequence<0)throw Error('Invalid synthetic transport actor');
 return {latitude:9.08+(slot%20)*0.0004+Math.sin(sequence)*0.0002,longitude:7.45+(Math.floor(slot/20)%20)*0.0004};
}
export function isTransportActor(actor){return actor.role==='provider'&&Number.isInteger(actor.slot)&&actor.slot%5===0;}
export function nearbyContentValid(data,radius,kind='all'){
 if(!data||!Array.isArray(data.items)||data.approximate_distances!==true)return false;
 let previous=-Infinity;const ids=new Set();
 for(const row of data.items){
  if(!row||typeof row.id!=='string'||ids.has(row.id)||row.nearby_driver!==true||row.contact_available!==true)return false;
  ids.add(row.id);
  // Public distances are rounded to 0.5km, so allow that rounding at radius.
  if(!Number.isFinite(row.distance_km)||row.distance_km<0||row.distance_km>radius+0.25||row.distance_km<previous)return false;
  previous=row.distance_km;
  if(!['passenger','courier','delivery'].includes(row.transport_kind)||(kind!=='all'&&row.transport_kind!==kind))return false;
  if(!Number.isFinite(row.fresh_for_seconds)||row.fresh_for_seconds<0||row.fresh_for_seconds>120)return false;
  if(['latitude','longitude','accuracy_m'].some(key=>key in row))return false;
 }
 return true;
}
