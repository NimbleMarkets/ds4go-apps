package render

import (
	"bytes"

	"github.com/soypat/gsdf/glbuild"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// generatedGLSL returns gsdf's GLSL function declarations for s, plus the name
// of the top-level SDF function. This is the transpiler's input.
func generatedGLSL(s simplesdf.SDF3) (glsl string, topName string, err error) {
	var buf bytes.Buffer
	prog := glbuild.NewDefaultProgrammer()
	name, _, _, err := prog.WriteSDFDecl(&buf, s.Shader())
	if err != nil {
		return "", "", err
	}
	return buf.String(), name, nil
}
