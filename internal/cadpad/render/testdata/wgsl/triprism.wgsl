fn gsdfEqTri(p_in: vec2<f32>, h: f32) -> f32 {
var p = p_in;
let k = sqrt(3.0);
p.x = abs(p.x) - h;
p.y = p.y + h/k;
if ( p.x+k*p.y>0.0 ) { p = vec2<f32>(p.x-k*p.y,-k*p.x-p.y)/2.0; }
p.x -= clamp( p.x, -2.0*h, 0.0 );
return -length(p)*sign(p.y);

}
fn circle2p(p: vec2<f32>) -> f32 {
return gsdfEqTri(p,1.154700518);

}
fn extrusion_circle2p(p: vec3<f32>) -> f32 {
var h=2.0;
var d=circle2p(p.xy);
var w = vec2<f32>( d, abs(p.z) - h );
return min(max(w.x,w.y),0.0) + length(max(w,0.0));

}
fn sdf(p: vec3<f32>) -> f32 { return extrusion_circle2p(p); }
