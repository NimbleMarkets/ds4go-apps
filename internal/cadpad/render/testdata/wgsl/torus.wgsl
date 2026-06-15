fn gsdfTorus3D(p: vec3<f32>, t1: f32, t2: f32) -> f32 {
var t = vec2<f32>(t1, t2);
var q = vec2<f32>(length(p.xz)-t.x,p.y);
return length(q)-t.y;

}
fn torus1p3p(p: vec3<f32>) -> f32 {
return gsdfTorus3D(p.xzy,3.0,1.0);

}
fn sdf(p: vec3<f32>) -> f32 { return torus1p3p(p); }
