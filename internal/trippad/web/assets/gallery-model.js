// Preserve the server's newest-first order while grouping named shader histories.
export function shaderGroups(entries) {
 const groups = new Map();
 for (const entry of entries) {
  const key = `${entry.kind}:${entry.name.trim().replace(/\s+/g, ' ').toLowerCase()}`;
  if (!groups.has(key)) groups.set(key, {key, name:entry.name, entries:[]});
  groups.get(key).entries.push(entry);
 }
 return [...groups.values()];
}

export function galleryView(entries, checked, {query = '', includeErrors = false, groupKey = null} = {}) {
 const groups = shaderGroups(entries);
 const search = query.trim().toLowerCase();
 const visible = entry => checked.has(entry.id) && (includeErrors || !checked.get(entry.id).error);
 const matches = entry => `${entry.name} ${entry.id} ${entry.kind} ${entry.saved || ''}`.toLowerCase().includes(search);
 const selected = groupKey === null ? null : groups.find(group => group.key === groupKey);
 if (groupKey !== null) {
  return {selected, cards:(selected?.entries || []).filter(entry => visible(entry) && matches(entry)).map(entry => ({entry, version:selected.entries.length-selected.entries.indexOf(entry)}))};
 }
 const cards = [];
 for (const group of groups) {
  const versions = group.entries.filter(visible);
  if (!versions.some(matches)) continue;
  // Even when errors are shown, the overview's load action picks working code.
  const entry = versions.find(entry => !checked.get(entry.id).error) || versions[0];
  cards.push({group, entry, visibleCount:versions.length, hiddenCount:group.entries.filter(entry => checked.get(entry.id)?.error && !includeErrors).length});
 }
 return {selected:null, cards};
}
