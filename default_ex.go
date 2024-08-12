package jet

import (
	"reflect"
)

var defaultExtensions = []string{
	"", // in case the path is given with the correct extension already
	".jet",
	".html.jet",
	".jet.html",
}

func SetDefaultExtensions(exts ...string) {
	defaultExtensions = exts
}

func AddDefaultExtensions(exts ...string) {
	defaultExtensions = append(defaultExtensions, exts...)
}

func AddDefaultVariables(values map[string]interface{}) {
	for name, value := range values {
		defaultVariables[name] = reflect.ValueOf(value)
	}
}

func (s *Set) SetDefaultExtensions(exts ...string) *Set {
	s.extensions = exts
	return s
}

func (s *Set) AddDefaultExtensions(exts ...string) *Set {
	s.extensions = append(s.extensions, exts...)
	return s
}

func (s *Set) SetDevelopmentMode(on bool) *Set {
	s.developmentMode = on
	return s
}
