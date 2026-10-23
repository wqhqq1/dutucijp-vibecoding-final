package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"returnsrecon/internal/apicontract"
	"sort"
	"strings"
)

func main() {
	root := "../docs/api"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if e := os.MkdirAll(root, 0755); e != nil {
		panic(e)
	}
	b, e := json.MarshalIndent(apicontract.Document(), "", "  ")
	if e != nil {
		panic(e)
	}
	if e = os.WriteFile(filepath.Join(root, "openapi.json"), append(b, '\n'), 0644); e != nil {
		panic(e)
	}
	var value any
	if e = json.Unmarshal(b, &value); e != nil {
		panic(e)
	}
	var out strings.Builder
	yaml(&out, value, 0)
	if e = os.WriteFile(filepath.Join(root, "openapi.yaml"), []byte(out.String()), 0644); e != nil {
		panic(e)
	}
	fmt.Println("OpenAPI 3.1 JSON/YAML generated")
}
func scalar(v any) string { b, _ := json.Marshal(v); return string(b) }
func yaml(out *strings.Builder, v any, indent int) {
	pad := strings.Repeat(" ", indent)
	switch x := v.(type) {
	case map[string]any:
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch child := x[k].(type) {
			case map[string]any:
				if len(child) == 0 {
					fmt.Fprintf(out, "%s%s: {}\n", pad, scalar(k))
				} else {
					fmt.Fprintf(out, "%s%s:\n", pad, scalar(k))
					yaml(out, child, indent+2)
				}
			case []any:
				if len(child) == 0 {
					fmt.Fprintf(out, "%s%s: []\n", pad, scalar(k))
				} else {
					fmt.Fprintf(out, "%s%s:\n", pad, scalar(k))
					yaml(out, child, indent+2)
				}
			default:
				fmt.Fprintf(out, "%s%s: %s\n", pad, scalar(k), scalar(child))
			}
		}
	case []any:
		for _, item := range x {
			switch child := item.(type) {
			case map[string]any:
				if len(child) == 0 {
					fmt.Fprintf(out, "%s- {}\n", pad)
				} else {
					fmt.Fprintf(out, "%s-\n", pad)
					yaml(out, child, indent+2)
				}
			case []any:
				fmt.Fprintf(out, "%s-\n", pad)
				yaml(out, child, indent+2)
			default:
				fmt.Fprintf(out, "%s- %s\n", pad, scalar(item))
			}
		}
	}
}
