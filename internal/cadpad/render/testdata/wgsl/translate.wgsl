fn sphere3p(p: vec3<f32>) -> f32 {
return length(p)-3.0;

}
fn translate1p2p3p_sphere3p(p: vec3<f32>) -> f32 {
var t=vec3<f32>(1.0,2.0,3.0);
return sphere3p(p-t);

}
fn sdf(p: vec3<f32>) -> f32 { return translate1p2p3p_sphere3p(p); }
