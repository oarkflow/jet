package main

import (
	"fmt"
	"time"

	"github.com/oarkflow/jet"
)

var (
	data3 = map[string]any{
		"first_name": "Sujit",
		"address": map[string]any{
			"city": "Kathmandu",
		},
	}
	data4 = map[string]any{
		"first_name": "Anita",
		"address": map[string]any{
			"city": "Delhi",
		},
	}
)

func main() {
	jet.DefaultSet(jet.WithDelims("<", ">"))
	jetParse()
	exprParse()
	// jetTemplateParse()
}

func jetParse() {
	start := time.Now()
	fmt.Println(jet.Parse("Hi Mr. <address.city>", data3))
	fmt.Println(jet.Parse("Hi Mr. <address.city>", data4))
	fmt.Println(fmt.Sprintf("%s", time.Since(start)))
}

func exprParse() {
	start := time.Now()
	fmt.Println(jet.Placeholders("Hi Mr. <address.city>"))
	fmt.Println(fmt.Sprintf("%s", time.Since(start)))
}

func jetTemplateParse() {
	start := time.Now()
	tmpl, err := jet.NewTemplate("Hi Mr. <address.city>")
	if err != nil {
		panic(err)
	}
	fmt.Println(tmpl.Parse(data3))
	fmt.Println(tmpl.Parse(data4))
	fmt.Println(fmt.Sprintf("%s", time.Since(start)))
}
