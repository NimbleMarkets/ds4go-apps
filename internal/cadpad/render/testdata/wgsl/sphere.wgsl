fn sphere3p(p: vec3<f32>) -> f32 {
return length(p)-3.0;

}
fn sdf(p: vec3<f32>) -> f32 { return sphere3p(p); }
