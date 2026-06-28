package exampleutil

import rules "github.com/cpcf/gess/rules"

func Fields(pairs ...any) rules.Fields {
	return rules.MustFields(pairs...)
}
