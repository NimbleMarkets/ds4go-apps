fn gsdfBoxFrame3D(p_in: vec3<f32>, x: f32, y: f32, z: f32, thick: f32) -> f32 {
var p = p_in;
var dims = vec3<f32>(x,y,z);
p = abs(p)-dims;
var q = abs(p+thick)-thick;
return min(min(
      length(max(vec3<f32>(p.x,q.y,q.z),0.0))+min(max(p.x,max(q.y,q.z)),0.0),
      length(max(vec3<f32>(q.x,p.y,q.z),0.0))+min(max(q.x,max(p.y,q.z)),0.0)),
      length(max(vec3<f32>(q.x,q.y,p.z),0.0))+min(max(q.x,max(q.y,p.z)),0.0));

}
fn boxframe4p3p2p0p150000006(p: vec3<f32>) -> f32 {
return gsdfBoxFrame3D(p,1.700000048,1.200000048,0.699999988,0.150000006);

}
fn sdf(p: vec3<f32>) -> f32 { return boxframe4p3p2p0p150000006(p); }
