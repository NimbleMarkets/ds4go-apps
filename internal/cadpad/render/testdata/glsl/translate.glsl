float sphere3p(vec3 p){
return length(p)-3.;
}
float translate1p2p3p_sphere3p(vec3 p){
vec3 t=vec3(1.,2.,3.);
return sphere3p(p-t);
}
