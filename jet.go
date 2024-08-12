package jet

type Delims struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

func Sprintf(format string, a any, delims ...*Delims) string {
	delim := &Delims{Left: "{", Right: "}"}
	if len(delims) > 0 {
		if delims[0].Left != "" {
			delim.Left = delims[0].Left
		}
		if delims[0].Right != "" {
			delim.Right = delims[0].Right
		}
	}
	set := NewMemorySet(WithDelims(delim.Left, delim.Right))
	tmpl, err := set.ParseContent(format)
	if err != nil {
		return format
	}
	rs, err := tmpl.ParseMap(a)
	if err != nil {
		return format
	}
	return rs
}
