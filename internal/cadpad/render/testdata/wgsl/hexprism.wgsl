fn gsdfHexagon3D(p_in: vec3<f32>, side: f32, extrude: f32) -> f32 {
var p = p_in;
var h = vec2<f32>(side, extrude);
let k = vec3<f32>(-0.8660254038, 0.5, 0.57735);
p = abs(p);
{ let _swz = (2.0*min(dot(k.xy, p.xy), 0.0)*k.xy); p.x = p.x - (_swz).x; p.y = p.y - (_swz).y; }
var aux = p.xy-vec2<f32>(clamp(p.x,-k.z*h.x,k.z*h.x), h.x);
var d = vec2<f32>( length(aux)*sign(p.y-h.x), p.z-h.y );
return min(max(d.x,d.y),0.0) + length(max(d,0.0));

}
fn hex2p4p(p: vec3<f32>) -> f32 {
return gsdfHexagon3D(p,2.0,4.0);

}
fn sdf(p: vec3<f32>) -> f32 { return hex2p4p(p); }
