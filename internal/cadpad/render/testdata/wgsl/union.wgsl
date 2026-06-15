fn gsdfBox3D(p: vec3<f32>, x: f32, y: f32, z: f32, round: f32) -> f32 {
var dims = vec3<f32>(x,y,z);
var q = abs(p)-dims+round;
return length(max(q,0.0)) + min(max(q.x,max(q.y,q.z)),0.0)-round;

}
fn box2p2p2p0p(p: vec3<f32>) -> f32 {
return gsdfBox3D(p,1.0,1.0,1.0,0.0);

}
fn sphere3p(p: vec3<f32>) -> f32 {
return length(p)-3.0;

}
fn union_sphere3p_box2p2p2p0p(p: vec3<f32>) -> f32 {
var d=sphere3p(p);
d=min(d,box2p2p2p0p(p));
return d;

}
fn sdf(p: vec3<f32>) -> f32 { return union_sphere3p_box2p2p2p0p(p); }
