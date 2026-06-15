package render

// kernelTemplate is the fixed raymarch+shade compute shader. %SDF% is replaced
// by the generated/ported `fn sdf(p: vec3<f32>) -> f32 { ... }` plus any helper
// prelude. Output is rgba8 packed into a u32 storage buffer, one per pixel.
const kernelTemplate = `
struct Cam {
    eye: vec3<f32>, _p0: f32,
    fwd: vec3<f32>, _p1: f32,
    right: vec3<f32>, _p2: f32,
    up: vec3<f32>, tanHalfFov: f32,
    w: u32, h: u32, _p3: u32, _p4: u32,
};
@group(0) @binding(0) var<uniform> cam: Cam;
@group(0) @binding(1) var<storage, read_write> out: array<u32>;

%SDF%

fn sdfNormal(p: vec3<f32>) -> vec3<f32> {
    let e = 0.001;
    let dx = sdf(p + vec3<f32>(e,0.0,0.0)) - sdf(p - vec3<f32>(e,0.0,0.0));
    let dy = sdf(p + vec3<f32>(0.0,e,0.0)) - sdf(p - vec3<f32>(0.0,e,0.0));
    let dz = sdf(p + vec3<f32>(0.0,0.0,e)) - sdf(p - vec3<f32>(0.0,0.0,e));
    return normalize(vec3<f32>(dx,dy,dz));
}

fn pack(c: vec3<f32>) -> u32 {
    let r = u32(clamp(c.x,0.0,1.0)*255.0);
    let g = u32(clamp(c.y,0.0,1.0)*255.0);
    let b = u32(clamp(c.z,0.0,1.0)*255.0);
    return r | (g<<8u) | (b<<16u) | (255u<<24u);
}

@compute @workgroup_size(8,8)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= cam.w || gid.y >= cam.h) { return; }
    let u = (2.0*(f32(gid.x)+0.5)/f32(cam.w) - 1.0) * cam.tanHalfFov * f32(cam.w)/f32(cam.h);
    let v = (1.0 - 2.0*(f32(gid.y)+0.5)/f32(cam.h)) * cam.tanHalfFov;
    let dir = normalize(cam.fwd + u*cam.right + v*cam.up);
    var t = 0.0;
    var hit = false;
    for (var i = 0; i < 80; i = i + 1) {
        let p = cam.eye + t*dir;
        let d = sdf(p);
        if (d < 0.002) { hit = true; break; }
        t = t + d*0.95;
        if (t > 100.0) { break; }
    }
    var col = vec3<f32>(0.098,0.110,0.137); // bg 25,28,35
    if (hit) {
        let p = cam.eye + t*dir;
        let n = sdfNormal(p);
        let ld = normalize(vec3<f32>(0.5,0.7,0.6));
        let lambert = max(dot(n,ld),0.0);
        let shade = 0.25 + 0.75*lambert;
        col = vec3<f32>(0.85,0.95,1.0) * shade; // teal tint
    }
    out[gid.y*cam.w + gid.x] = pack(col);
}`

// spikeSphereSDF is a hardcoded WGSL sdf used only by the Phase 0 spike.
const spikeSphereSDF = `fn sdf(p: vec3<f32>) -> f32 { return length(p) - 3.0; }`
