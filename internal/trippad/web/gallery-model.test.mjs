import test from 'node:test';
import assert from 'node:assert/strict';
import {galleryView, shaderGroups} from './assets/gallery-model.js';

const entries = [
 {id:'bad',name:'Cave Rave',kind:'saved',saved:'2026-09-23'},
 {id:'good',name:' cave   rave ',kind:'saved',saved:'2026-09-22'},
 {id:'other',name:'Ocean',kind:'saved',saved:'2026-09-21'},
 {id:'old',name:'CAVE RAVE',kind:'saved',saved:'2026-09-20'},
 {id:'starter',name:'Cave Rave',kind:'starter'},
];
const checked = new Map(entries.map(entry => [entry.id,{error:entry.id === 'bad' ? 'invalid WGSL' : ''}]));

test('groups names despite spacing/case, preserves newest-first history and separates starters', () => {
 const groups = shaderGroups(entries);
 assert.equal(groups.length,3);
 assert.deepEqual(groups[0].entries.map(e => e.id),['bad','good','old']);
 assert.notEqual(groups[0].key,groups[2].key);
});
test('overview shows one card per shader and loads latest compatible version', () => {
 const {cards} = galleryView(entries,checked);
 assert.equal(cards.length,3);
 assert.equal(cards[0].entry.id,'good');
 assert.equal(cards[0].visibleCount,2);
 assert.equal(cards[0].hiddenCount,1);
 const includingErrors = galleryView(entries,checked,{includeErrors:true});
 assert.equal(includingErrors.cards[0].visibleCount,3);
 assert.equal(includingErrors.cards[0].entry.id,'good');
});
test('version drilldown preserves stable version numbers and error filtering', () => {
 const groupKey = shaderGroups(entries)[0].key;
 const hidden = galleryView(entries,checked,{groupKey});
 assert.deepEqual(hidden.cards.map(c => [c.entry.id,c.version]),[['good',2],['old',1]]);
 const shown = galleryView(entries,checked,{groupKey,includeErrors:true});
 assert.deepEqual(shown.cards.map(c => c.entry.id),['bad','good','old']);
 assert.equal(shown.cards[0].version,3);
});
test('historical IDs find their group; details can narrow to a specific version', () => {
 const result = galleryView(entries,checked,{query:'old'});
 assert.equal(result.cards.length,1);
 assert.equal(result.cards[0].entry.id,'good');
 const versions = galleryView(entries,checked,{groupKey:result.cards[0].group.key,query:'old'});
 assert.equal(versions.cards.length,1);
 assert.equal(versions.cards[0].entry.id,'old');
});
test('pending and all-broken groups stay hidden until available or explicitly included', () => {
 const failures = new Map([['bad',{error:'invalid'}]]);
 assert.equal(galleryView(entries,failures).cards.length,0);
 const result = galleryView(entries,failures,{includeErrors:true});
 assert.equal(result.cards.length,1);
 assert.equal(result.cards[0].entry.id,'bad');
 assert.equal(result.cards[0].visibleCount,1);
});
