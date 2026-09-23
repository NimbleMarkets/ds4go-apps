import {setupGallery} from './gallery.js';

const $ = id => document.getElementById(id);
const canvas = $('canvas');
let device, context, format, presenter, uniform, pipeline, output, computeGroup, presentGroup;
let state, values = [], time = 0, frame = 0, playing = true, dirty = true, loading = false, stopped = false;
let lastTick = 0, lastSubmit = 0, windowStart = 0, count = 0, frameLimit = 60;
let inFlight = 0;
let galleryOpen = false, gpuTasks = Promise.resolve();
// Error scopes belong to the device; serialize asynchronous compilation/preview work.
function gpuTask(task) {
 const result = gpuTasks.then(task);
 gpuTasks = result.catch(() => {});
 return result;
}
const bytes = new ArrayBuffer(96), floats = new Float32Array(bytes), uints = new Uint32Array(bytes);

// This pass only presents the packed output of the unchanged native compute shader.
const presentationWGSL = `
struct Uniforms {
 time: f32, dt: f32, w: u32, h: u32,
 frame: u32, pad0: u32, pad1: u32, pad2: u32,
 params: array<vec4<f32>, 4>,
}
@group(0) @binding(0) var<uniform> uni: Uniforms;
@group(0) @binding(1) var<storage, read> pixels: array<u32>;
@vertex fn vertex(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
 let positions = array<vec2<f32>,3>(vec2<f32>(-1.0,-1.0), vec2<f32>(3.0,-1.0), vec2<f32>(-1.0,3.0));
 return vec4<f32>(positions[i],0.0,1.0);
}
@fragment fn fragment(@builtin(position) p: vec4<f32>) -> @location(0) vec4<f32> {
 let c = pixels[u32(p.y)*uni.w+u32(p.x)];
 return vec4<f32>(f32(c & 255u),f32((c >> 8u) & 255u),f32((c >> 16u) & 255u),255.0)/255.0;
}`;

function status(message, error = false) {
 $('status').textContent = message;
 $('status').classList.toggle('error', error);
}
function resetMetrics() {
 lastTick = lastSubmit = count = 0;
 windowStart = performance.now();
 $('fps').textContent = playing ? '— fps' : 'Paused';
}
async function fetchState(url = '/api/state') {
 const response = await fetch(url);
 if (!response.ok) throw new Error(`State request failed: ${response.status}`);
 return response.json();
}
function controls() {
 $('controls').replaceChildren();
 (state.source.params || []).forEach((p, i) => {
  const row = document.createElement('div'), label = document.createElement('label');
  const range = document.createElement('input'), value = document.createElement('output');
  range.type = 'range'; range.min = p.min; range.max = p.max; range.step = p.step;
  range.value = values[i]; range.id = `param-${i}`;
  value.textContent = Number(values[i]).toPrecision(5);
  label.htmlFor = range.id; label.append(document.createTextNode(p.name), value);
  range.addEventListener('input', () => { values[i] = Number(range.value); value.textContent = values[i].toPrecision(5); dirty = true; });
  row.append(label, range); $('controls').append(row);
 });
}
function buffers(compute, w, h) {
 if (!Number.isInteger(w) || !Number.isInteger(h) || w < 1 || h < 1 || w > 4096 || h > 4096 || w > device.limits.maxTextureDimension2D || h > device.limits.maxTextureDimension2D || w*h*4 > device.limits.maxStorageBufferBindingSize) {
  throw new Error('Render size exceeds supported limits (1–4096 per dimension).');
 }
 const next = device.createBuffer({size: w*h*4, usage: GPUBufferUsage.STORAGE});
 const entries = [{binding: 0, resource: {buffer: uniform}}, {binding: 1, resource: {buffer: next}}];
 const cg = device.createBindGroup({layout: compute.getBindGroupLayout(0), entries});
 const pg = device.createBindGroup({layout: presenter.getBindGroupLayout(0), entries});
 return {next, cg, pg, w, h};
}
function commitBuffers(b) {
 output?.destroy(); output = b.next; computeGroup = b.cg; presentGroup = b.pg;
 canvas.width = b.w; canvas.height = b.h;
 $('size').textContent = `${b.w} × ${b.h}`;
 dirty = true; resetMetrics();
}
async function load(url = '/api/state') {
 if (loading) return false;
 loading = true; $('reload').disabled = true;
 try {
  const next = await fetchState(url);
  await gpuTask(async () => {
  device.pushErrorScope('validation');
  let compiled, compileError;
  try {
  const module = device.createShaderModule({code: next.wgsl});
  const info = await module.getCompilationInfo();
  const errors = info.messages.filter(m => m.type === 'error');
  if (errors.length) throw new Error(errors.map(m => `WGSL ${m.lineNum}:${m.linePos} ${m.message}`).join('\n'));
  compiled = await device.createComputePipelineAsync({layout: 'auto', compute: {module, entryPoint: 'main'}});
  } catch (error) { compileError = error; }
  const compileValidation = await device.popErrorScope();
  if (compileError || compileValidation) throw compileError || compileValidation;
  device.pushErrorScope('validation');
  let b, validation;
  try { b = buffers(compiled, next.width, next.height); }
  finally { validation = await device.popErrorScope(); }
  if (validation) { b?.next.destroy(); throw new Error(validation.message); }
  state = next; pipeline = compiled; commitBuffers(b);
  values = (state.source.params || []).map(p => state.values[p.name] ?? p.default);
  time = state.time; frame = state.frame;
  $('name').textContent = state.source.name;
  $('source').textContent = state.wgsl;
  frameLimit = state.target_fps;
  controls();
  status('Ready');
  });
  return true;
 } catch (error) { status(error.message, true); return false; }
 finally { loading = false; $('reload').disabled = false; lastTick = 0; }
}

function animate(now) {
 if (stopped) return;
 requestAnimationFrame(animate);
 if (document.hidden || loading || galleryOpen || !pipeline) { lastTick = 0; return; }
 const dt = lastTick ? (now-lastTick)/1000 : 0;
 lastTick = now;
 if (playing) time += dt;
 const limit = frameLimit;
 if (inFlight >= 2 || (!playing && !dirty) || (lastSubmit && limit && now-lastSubmit < 1000/limit-0.5)) return;
 const frameDt = playing && lastSubmit ? (now-lastSubmit)/1000 : 0;
 lastSubmit = now;
 floats[0] = time; floats[1] = frameDt;
 uints[2] = canvas.width; uints[3] = canvas.height; uints[4] = frame++;
 floats.fill(0, 8); floats.set(values, 8);
 device.queue.writeBuffer(uniform, 0, bytes);
 const encoder = device.createCommandEncoder();
 const pass = encoder.beginComputePass();
 pass.setPipeline(pipeline); pass.setBindGroup(0, computeGroup);
 pass.dispatchWorkgroups(Math.ceil(canvas.width/8), Math.ceil(canvas.height/8)); pass.end();
 const render = encoder.beginRenderPass({colorAttachments: [{view: context.getCurrentTexture().createView(), loadOp: 'clear', storeOp: 'store', clearValue: [0,0,0,1]}]});
 render.setPipeline(presenter); render.setBindGroup(0, presentGroup); render.draw(3); render.end();
 device.queue.submit([encoder.finish()]);
 // Bound queued GPU work so a slow shader cannot build an ever-growing backlog.
 inFlight++;
 device.queue.onSubmittedWorkDone().catch(error => {
  stopped = true; status(`GPU submission failed: ${error.message}`, true);
 }).finally(() => { inFlight--; });
 dirty = false; count++;
 if (!windowStart) windowStart = now;
 if (playing && now-windowStart >= 1000) {
  $('fps').textContent = `${(count*1000/(now-windowStart)).toFixed(1)} fps`;
  windowStart = now; count = 0;
 }
}

async function start() {
 if (!navigator.gpu) throw new Error('WebGPU is unavailable. Use a WebGPU-enabled browser on this localhost link.');
 const adapter = await navigator.gpu.requestAdapter();
 if (!adapter) throw new Error('No WebGPU adapter available. Check browser graphics settings.');
 device = await adapter.requestDevice();
 device.lost.then(info => { stopped = true; status(`GPU device lost: ${info.message || info.reason}. Reload the page to reconnect.`, true); });
 device.addEventListener('uncapturederror', event => { stopped = true; status(event.error.message, true); });
 context = canvas.getContext('webgpu');
 format = navigator.gpu.getPreferredCanvasFormat();
 context.configure({device, format, alphaMode: 'opaque'});
 uniform = device.createBuffer({size: 96, usage: GPUBufferUsage.UNIFORM | GPUBufferUsage.COPY_DST});
 const module = device.createShaderModule({code: presentationWGSL});
 presenter = await device.createRenderPipelineAsync({layout: 'auto', vertex: {module, entryPoint: 'vertex'}, fragment: {module, entryPoint: 'fragment', targets: [{format}]}});
 $('reload').onclick = () => load();
 $('play').onclick = () => { playing = !playing; $('play').textContent = playing ? 'Pause' : 'Play'; resetMetrics(); };
 $('reset').onclick = () => { time = frame = 0; dirty = true; resetMetrics(); };
 setupGallery({device, presenter, format, gpuTask, load, setOpen: open => {
  galleryOpen = open; resetMetrics();
 }});
 document.addEventListener('visibilitychange', resetMetrics);
 await load();
 requestAnimationFrame(animate);
}
start().catch(error => status(error.message, true));
