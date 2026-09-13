fn gsdfBox3D(p: vec3<f32>, x: f32, y: f32, z: f32, round: f32) -> f32 {
var dims = vec3<f32>(x,y,z);
var q = abs(p)-dims+round;
return length(max(q,vec3<f32>(0.0))) + min(max(q.x,max(q.y,q.z)),0.0)-round;

}
fn box4p3p2p0p(p: vec3<f32>) -> f32 {
return gsdfBox3D(p,2.0,1.5,1.0,0.0);

}
fn sdf(p: vec3<f32>) -> f32 { return box4p3p2p0p(p); }
