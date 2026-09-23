package shader

import "github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"

// Starters returns independent source definitions; callers may freely edit them.
func Starters() []Source {
	common := func() []params.Param {
		return []params.Param{
			{Name: "speed", Min: 0, Max: 3, Step: 0.05, Default: 0.6},
			{Name: "scale", Min: 0.2, Max: 8, Step: 0.1, Default: 2.5},
			{Name: "color", Min: 0, Max: 1, Step: 0.01, Default: 0.1},
			{Name: "warp", Min: 0, Max: 3, Step: 0.05, Default: 0.8},
		}
	}
	return []Source{
		{Name: "plasma", Params: common(), ShadeBody: `let t=uni.time*p_speed(); let p=uv*p_scale();
 let v=sin(p.x*3.0+t)+sin(p.y*4.0-t)+sin(length(p)*3.0+t*p_warp());
 return palette(v*0.18+p_color());`},
		{Name: "kaleidoscope", Params: common(), ShadeBody: `let t=uni.time*p_speed(); let p=rot2d(t*0.2)*uv*p_scale();
 let a=atan2(p.y,p.x); let r=length(p);
 let v=sin(cos(a*8.0)*r*3.0+p_warp()*sin(r*4.0-t))+cos(r*5.0-t);
 return palette(v*0.25+p_color()+t*0.07);`},
		{Name: "tunnel", Params: common(), ShadeBody: `let t=uni.time*p_speed(); let p=uv*p_scale(); let r=max(length(p),0.03);
 let a=atan2(p.y,p.x); let v=1.0/r+t+p_warp()*sin(a*6.0+t);
 return palette(v*0.25+p_color())*smoothstep(0.03,0.4,r);`},
		{Name: "fbm-warp", Params: common(), ShadeBody: `let t=uni.time*p_speed(); let p=uv*p_scale();
 let q=vec2<f32>(fbm(p+vec2<f32>(t*0.2,0.0)),fbm(p+vec2<f32>(4.7,-t*0.15)));
 let v=fbm(p+q*p_warp()*4.0+vec2<f32>(t*0.1));
 return palette(v*2.0+p_color());`},
	}
}
