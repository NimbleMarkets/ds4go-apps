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
    w: u32, h: u32, samples: u32, maxSteps: u32,
    eps: f32, farT: f32, _q0: f32, _q1: f32,
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

// shadeRay marches one primary ray and returns its shaded color. The march
// parameters (eps/maxSteps/farT) come from the Cam uniform so the host can
// trade speed for quality; with the default quality (eps=0.002, maxSteps=80,
// farT=100) this is bit-for-bit identical to the original single-ray body.
fn shadeRay(u: f32, v: f32) -> vec3<f32> {
    let dir = normalize(cam.fwd + u*cam.right + v*cam.up);
    var t = 0.0;
    var hit = false;
    for (var i = 0u; i < cam.maxSteps; i = i + 1u) {
        let p = cam.eye + t*dir;
        let d = sdf(p);
        if (d < cam.eps) { hit = true; break; }
        t = t + d*0.95;
        if (t > cam.farT) { break; }
    }
    var col = vec3<f32>(0.098,0.110,0.137); // bg 25,28,35
    if (hit) {
        let p = cam.eye + t*dir;
        let n = sdfNormal(p);
        // Match raycast.go's lightDir = normalize(1,1,1) and lambert shade.
        let ld = normalize(vec3<f32>(1.0,1.0,1.0));
        let lambert = max(dot(n,ld),0.0);
        let shade = min(0.25 + 0.75*lambert, 1.0);
        col = vec3<f32>(0.85,0.95,1.0) * shade; // teal tint
    }
    return col;
}

@compute @workgroup_size(8,8)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    if (gid.x >= cam.w || gid.y >= cam.h) { return; }
    let s = max(cam.samples, 1u);
    let fs = f32(s);
    // Ordered-grid supersampling: shoot s*s rays at evenly spaced sub-pixel
    // offsets and average the shaded color. For s==1 the single offset is
    // ((0+0.5)/1, (0+0.5)/1) = (0.5, 0.5) — the original pixel-center sample,
    // and the u/v expressions below are byte-identical to the original body.
    var acc = vec3<f32>(0.0,0.0,0.0);
    for (var sy = 0u; sy < s; sy = sy + 1u) {
        for (var sx = 0u; sx < s; sx = sx + 1u) {
            let ox = (f32(sx)+0.5)/fs;
            let oy = (f32(sy)+0.5)/fs;
            let u = (2.0*(f32(gid.x)+ox)/f32(cam.w) - 1.0) * cam.tanHalfFov * f32(cam.w)/f32(cam.h);
            let v = (1.0 - 2.0*(f32(gid.y)+oy)/f32(cam.h)) * cam.tanHalfFov;
            acc = acc + shadeRay(u, v);
        }
    }
    let col = acc / (fs*fs);
    out[gid.y*cam.w + gid.x] = pack(col);
}`
