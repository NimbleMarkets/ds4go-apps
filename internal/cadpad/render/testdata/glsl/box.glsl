
float gsdfBox3D(vec3 p, float x, float y, float z, float round) {
vec3 dims = vec3(x,y,z);
vec3 q = abs(p)-dims+round;
return length(max(q,0.0)) + min(max(q.x,max(q.y,q.z)),0.0)-round;
}
float box4p3p2p0p(vec3 p){
return gsdfBox3D(p,2.,1.5,1.,0.);
}
