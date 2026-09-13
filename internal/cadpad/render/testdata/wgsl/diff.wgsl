fn gsdfBox3D(p: vec3<f32>, x: f32, y: f32, z: f32, round: f32) -> f32 {
var dims = vec3<f32>(x,y,z);
var q = abs(p)-dims+round;
return length(max(q,vec3<f32>(0.0))) + min(max(q.x,max(q.y,q.z)),0.0)-round;

}
fn box2p2p2p0p(p: vec3<f32>) -> f32 {
return gsdfBox3D(p,1.0,1.0,1.0,0.0);

}
fn sphere3p(p: vec3<f32>) -> f32 {
return length(p)-3.0;

}
fn diff_sphere3p_box2p2p2p0p(p: vec3<f32>) -> f32 {
var a=sphere3p(p);
var b=box2p2p2p0p(p);
return max(a,-b);

}
fn sdf(p: vec3<f32>) -> f32 { return diff_sphere3p_box2p2p2p0p(p); }
