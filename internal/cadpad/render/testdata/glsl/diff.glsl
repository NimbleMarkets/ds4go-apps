
float gsdfBox3D(vec3 p, float x, float y, float z, float round) {
vec3 dims = vec3(x,y,z);
vec3 q = abs(p)-dims+round;
return length(max(q,0.0)) + min(max(q.x,max(q.y,q.z)),0.0)-round;
}
float box2p2p2p0p(vec3 p){
return gsdfBox3D(p,1.,1.,1.,0.);
}
float sphere3p(vec3 p){
return length(p)-3.;
}
float diff_sphere3p_box2p2p2p0p(vec3 p){
float a=sphere3p(p);
float b=box2p2p2p0p(p);
return max(a,-b);
}
