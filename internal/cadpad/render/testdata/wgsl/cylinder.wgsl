fn gsdfCylinder3D(p_in: vec3<f32>, radius: f32, h: f32, round: f32) -> f32 {
var p = p_in;
p = p.xzy;
var d = vec2<f32>( length(p.xz)-radius+round, abs(p.y) - h );
return min(max(d.x,d.y),0.0) + length(max(d,0.0)) - round;

}
fn cyl2p5p0p(p: vec3<f32>) -> f32 {
return gsdfCylinder3D(p,2.0,2.5,0.0);

}
fn sdf(p: vec3<f32>) -> f32 { return cyl2p5p0p(p); }
