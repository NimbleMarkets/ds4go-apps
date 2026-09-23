// Gallery thumbnails use the same compute shader at a fixed clock and small size.
// Main rendering pauses while the modal is open, keeping preview cost out of FPS.
import {galleryView, shaderGroups} from './gallery-model.js';

export function setupGallery({device, presenter, format, gpuTask, load, setOpen}) {
 const $ = id => document.getElementById(id), dialog = $('gallery-dialog');
 let entries = [], page = 0, revision = 0, refreshRevision = 0, contexts = [], note = '';
 const checked = new Map();
 let checking = false, checkedCount = 0;
 let groupKey = null, overviewPage = 0, overviewQuery = '';
 const pageSize = 6;
 function cleanup() {
  revision++;
  for (const context of contexts) context.unconfigure();
  contexts = [];
 }
 function close() { refreshRevision++; cleanup(); setOpen(false); }
 dialog.addEventListener('close', close);
 $('gallery-close').onclick = () => dialog.close();
 $('gallery-search').oninput = () => { page = 0; showPage(); };
 $('gallery-errors').onchange = () => { page = 0; showPage(); };
 $('gallery-back').onclick = () => {
  groupKey = null; page = overviewPage; $('gallery-search').value = overviewQuery; showPage();
  dialog.scrollTop = 0; $('gallery-search').focus();
 };
 $('gallery-prev').onclick = () => { page--; showPage(); };
 $('gallery-next').onclick = () => { page++; showPage(); };
 $('gallery-refresh').onclick = refresh;
 $('gallery-open').onclick = () => {
  dialog.showModal(); setOpen(true); refresh();
 };
 async function get(url) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Gallery request failed (${response.status})`);
  return response.json();
 }
 async function refresh() {
  const request = ++refreshRevision;
  $('gallery-note').textContent = 'Loading gallery…';
  try {
   const result = await get('/api/gallery');
   if (request !== refreshRevision || !dialog.open) return;
   entries = result.entries;
   note = result.warnings?.length ? `${result.warnings.length} unreadable preset(s) skipped.\n${result.warnings.slice(0,3).join('\n')}` : '';
   page = 0; checking = true; checkedCount = 0; showPage();
   for (const entry of entries) {
    if (request !== refreshRevision || !dialog.open) return;
    if (!checked.has(entry.id)) {
     const snapshot = await get(`/api/gallery/${encodeURIComponent(entry.id)}`);
     const error = await gpuTask(async () => {
      if (request !== refreshRevision || !dialog.open) return null;
      device.pushErrorScope('validation');
      let failure;
      try {
       const module = device.createShaderModule({code:snapshot.wgsl});
       const info = await module.getCompilationInfo();
       const errors = info.messages.filter(m => m.type === 'error');
       if (errors.length) throw new Error(errors.map(m => `Line ${m.lineNum}: ${m.message}`).join('\n'));
       await device.createComputePipelineAsync({layout:'auto',compute:{module,entryPoint:'main'}});
      } catch (error) { failure = error; }
      const validation = await device.popErrorScope();
      return (failure || validation)?.message || '';
     });
     if (request !== refreshRevision || !dialog.open) return;
     checked.set(entry.id, {snapshot, error});
    }
    checkedCount++;
    // Reveal compatible entries while checking the rest, without rerendering
    // thumbnails on every compilation. Results are cached by immutable ID.
    if (checkedCount % 12 === 0) showPage();
    else updateNote();
   }
   checking = false; showPage();
  } catch (error) {
   if (request === refreshRevision) { checking = false; $('gallery-note').textContent = error.message; }
  }
 }
 function updateNote() {
  const scope = groupKey === null ? entries : shaderGroups(entries).find(group => group.key === groupKey)?.entries || [];
  const failures = scope.filter(e => checked.get(e.id)?.error).length;
  const progress = checking ? `Checking browser compiler compatibility… ${checkedCount}/${entries.length}. ` : '';
  const excluded = failures ? `${failures} ${failures === 1 ? 'version' : 'versions'} with compiler errors ${$('gallery-errors').checked ? 'included' : 'hidden'}. ` : '';
  $('gallery-note').textContent = progress + excluded + (note || 'Static previews at t = 2s · saved parameter values');
 }
 function showPage() {
  cleanup();
  const version = revision;
  const query = $('gallery-search').value.trim().toLowerCase();
  const view = galleryView(entries, checked, {query, includeErrors:$('gallery-errors').checked, groupKey});
  const matches = view.cards;
  $('gallery-back').hidden = groupKey === null;
  $('gallery-title').textContent = groupKey === null ? 'Shader gallery' : view.selected?.name || 'Shader versions';
  $('gallery-description').textContent = groupKey === null ? 'One card per shader. Load the latest compatible version or browse its history.' : 'Versions are ordered newest first. Loading restores that version’s shader and saved controls.';
  const pages = Math.max(1, Math.ceil(matches.length/pageSize));
  page = Math.max(0, Math.min(page, pages-1));
  $('gallery-prev').disabled = page === 0;
  $('gallery-next').disabled = page === pages-1;
  const noun = groupKey === null ? 'shader group' : 'version';
  $('gallery-page').textContent = `${matches.length} ${noun}${matches.length === 1 ? '' : 's'} · Page ${page+1} of ${pages}`;
  updateNote();
  $('gallery-grid').replaceChildren();
  const jobs = [];
  if (!matches.length) {
   const empty = document.createElement('p'); empty.className = 'muted';
   empty.textContent = checking ? 'Checking saved shaders…' : 'No matching shaders. Try another search or include versions with compiler errors.';
   $('gallery-grid').append(empty);
  }
  for (const item of matches.slice(page*pageSize, (page+1)*pageSize)) {
   const {entry, group} = item;
   const card = document.createElement('article'), canvas = document.createElement('canvas');
   card.className = 'gallery-card'; canvas.width = 160; canvas.height = 96;
   canvas.setAttribute('aria-label', `${entry.name} preview`);
   const body = document.createElement('div'), title = document.createElement('h3');
   body.className = 'card-body'; title.textContent = group ? group.name : `Version ${item.version}`;
   const metadata = document.createElement('p'), previewStatus = document.createElement('p');
   metadata.textContent = `${entry.kind === 'starter' ? 'Built-in starter' : `${group ? 'Latest compatible · ' : ''}${new Date(entry.saved).toLocaleString()}`} · ${entry.controls} controls · ${entry.id.replace(/^starter-/, '').slice(0,8)}`;
   const compileError = checked.get(entry.id).error;
   previewStatus.textContent = compileError ? `Compiler error: ${compileError}` : 'Preparing preview…';
   if (compileError && group) metadata.textContent = `${group.entries.length} saved version(s) · no compatible version`;
   const button = document.createElement('button');
   const loadLabel = group && entry.kind !== 'starter' ? 'Load latest' : 'Load shader';
   button.textContent = compileError ? 'Compiler error' : loadLabel; button.disabled = Boolean(compileError);
   const url = `/api/gallery/${encodeURIComponent(entry.id)}`;
   button.onclick = async () => {
    // Cancel pending thumbnails before queuing a main-pipeline replacement.
    cleanup(); button.disabled = true; button.textContent = 'Loading…';
    const ok = await load(url);
    if (ok) dialog.close();
    else { previewStatus.textContent = 'Could not compile/load this shader; see the main status for details.'; button.disabled = false; button.textContent = loadLabel; }
   };
   body.append(title);
   if (group) {
    const count = document.createElement('p'); count.className = 'version-count';
    count.textContent = `${group.entries.length} ${group.entries.length === 1 ? 'version' : 'versions'}${item.hiddenCount ? ` · ${item.hiddenCount} with errors hidden` : ''}`;
    body.append(count);
   }
   body.append(metadata, previewStatus, button);
   if (group && entry.kind !== 'starter') {
    const history = document.createElement('button'); history.className = 'version-history';
    history.textContent = `View versions (${group.entries.length})`;
    history.onclick = () => {
     overviewPage = page; overviewQuery = $('gallery-search').value;
     groupKey = group.key; page = 0; $('gallery-search').value = ''; showPage();
     dialog.scrollTop = 0; $('gallery-back').focus();
    };
    body.append(history);
   }
   card.append(canvas, body); $('gallery-grid').append(card);
   if (!compileError) jobs.push({url, canvas, previewStatus, snapshot:checked.get(entry.id).snapshot});
  }
  // Compile/render one small preview at a time; cancelled pages do no more GPU work.
  (async () => {
   for (const job of jobs) {
    if (version !== revision || !dialog.open) return;
    try {
     const snapshot = job.snapshot;
     await gpuTask(async () => {
      if (version !== revision || !dialog.open) return;
      await preview(snapshot, job.canvas, version);
     });
     if (version === revision) job.previewStatus.textContent = 'Preview ready';
    } catch (error) {
     if (version === revision) job.previewStatus.textContent = `Preview unavailable: ${error.message}`;
    }
   }
  })();
 }
 async function preview(snapshot, canvas, version) {
  let uniform, pixels;
  device.pushErrorScope('validation');
  let error;
  try {
   const module = device.createShaderModule({code: snapshot.wgsl});
   const info = await module.getCompilationInfo();
   const errors = info.messages.filter(m => m.type === 'error');
   if (errors.length) throw new Error(errors.map(m => m.message).join('\n'));
   const pipeline = await device.createComputePipelineAsync({layout: 'auto', compute: {module, entryPoint: 'main'}});
   if (version !== revision || !dialog.open) return;
   const context = canvas.getContext('webgpu'); context.configure({device, format, alphaMode:'opaque'}); contexts.push(context);
   const bytes = new ArrayBuffer(96), f = new Float32Array(bytes), u = new Uint32Array(bytes);
   f[0] = 2; f[1] = 1/60; u[2] = canvas.width; u[3] = canvas.height; u[4] = 120;
   (snapshot.source.params || []).forEach((p,i) => { f[8+i] = snapshot.values[p.name] ?? p.default; });
   uniform = device.createBuffer({size:96, usage:GPUBufferUsage.UNIFORM | GPUBufferUsage.COPY_DST});
   pixels = device.createBuffer({size:canvas.width*canvas.height*4, usage:GPUBufferUsage.STORAGE});
   device.queue.writeBuffer(uniform,0,bytes);
   const bindings = [{binding:0,resource:{buffer:uniform}}, {binding:1,resource:{buffer:pixels}}];
   const computeGroup = device.createBindGroup({layout:pipeline.getBindGroupLayout(0),entries:bindings});
   const presentGroup = device.createBindGroup({layout:presenter.getBindGroupLayout(0),entries:bindings});
   const encoder = device.createCommandEncoder(), compute = encoder.beginComputePass();
   compute.setPipeline(pipeline); compute.setBindGroup(0,computeGroup); compute.dispatchWorkgroups(Math.ceil(canvas.width/8),Math.ceil(canvas.height/8)); compute.end();
   const render = encoder.beginRenderPass({colorAttachments:[{view:context.getCurrentTexture().createView(),loadOp:'clear',storeOp:'store',clearValue:[0,0,0,1]}]});
   render.setPipeline(presenter); render.setBindGroup(0,presentGroup); render.draw(3); render.end();
   device.queue.submit([encoder.finish()]); await device.queue.onSubmittedWorkDone();
  } catch (e) { error = e; }
  finally {
   const validation = await device.popErrorScope();
   uniform?.destroy(); pixels?.destroy();
   if (error || validation) throw error || validation;
  }
 }
}
