package exampleutil

import "github.com/cpcf/gess"

func Fields(pairs ...any) gess.Fields {
	return gess.MustFields(pairs...)
}
