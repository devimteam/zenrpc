package main

import (
	"text/template"

	"github.com/devimteam/zenrpc/parser"
)

var (
	serviceTemplate = template.Must(template.New("service").
		Funcs(template.FuncMap{"definitions": parser.Definitions}).
		Parse(`
{{define "smdType" -}}
	Type: smd.{{.Type}},
	{{- if eq .Type "Array" }}
		Items: map[string]string{
			{{- if and (eq .ItemsType "Object") .Ref }}
			"$ref": "#/definitions/{{.Ref}}",
			{{else}}
			"type": smd.{{.ItemsType}},
			{{end}}
		},
	{{- end}}
{{- end}}

{{define "properties" -}}
	Properties: map[string]smd.Property{
	{{range $i, $e := . -}}
		"{{.Name}}": {
			Description: ` + "`{{.Description}}`" + `,
			{{- if and (eq .SMDType.Type "Object") .SMDType.Ref }}
				Ref: "#/definitions/{{.SMDType.Ref}}",
			{{- end}}			
			{{template "smdType" .SMDType}}
		},
	{{ end }}
	},
{{- end}}

{{define "definitions" -}}
	{{if .}}
	Definitions: map[string]smd.Definition{
		{{- range .}}
			"{{ .Name }}": {
				Type: "object",
				{{ template "properties" .Properties}}
			},
		{{- end }}
	},
	{{ end }}
{{- end}}


// Code generated by zenrpc; DO NOT EDIT.

package {{.PackageName}}

import (
	"encoding/json"
	"context"
	"time"

	"github.com/devimteam/zenrpc"
	"github.com/devimteam/zenrpc/smd"

	{{ range .ImportsForGeneration}}
		{{if .Name}}{{.Name.Name}} {{end}}{{.Path.Value}}
	{{- end }}
)

var _ = time.Time{} // suspend 'imported but not used' error

var RPC = struct {
{{ range .Services}}
	{{.Name}} struct { {{range $i, $e := .Methods }}{{if $i}}, {{end}}{{.Name}}{{ end }} string } 
{{- end }}
}{	
	{{- range .Services}}
		{{.Name}}: struct { {{range $i, $e := .Methods }} {{if $i}}, {{end}}{{.Name}}{{ end }} string }{ 
			{{- range .Methods }}
				{{.Name}}:  "{{.EndpointName}}",
			{{- end }}
		}, 	
	{{- end }}
}

{{ range $s := .Services}}
	{{$isIface := .IsInterface}}
	{{if $isIface}}
	type {{.Name}}Server struct {
		S {{.Name}}
	}
	{{end}}

	func ({{.Name}}{{if $isIface}}Server{{end}}) SMD() smd.ServiceInfo {
		return smd.ServiceInfo{
			Description: ` + "`{{.Description}}`" + `,
			Methods: map[string]smd.Service{ 
				{{- range .Methods }}
					"{{.SchemaEndpointName}}": {
						Description: ` + "`{{.Description}}`" + `,
						Parameters: []smd.JSONSchema{ 
						{{- range .Args }}
							{
								Name: "{{.Name}}",
								Optional: {{or .HasStar .HasDefaultValue}},
								Description: ` + "`{{.Description}}`" + `,
								{{template "smdType" .SMDType}}
								{{- if and (eq .SMDType.Type "Object") (ne .SMDType.Ref "")}}
									{{ template "properties" (index $.Structs .SMDType.Ref).Properties}}
								{{- end}}
								{{- template "definitions" definitions .SMDType $.Structs }}
							},
						{{- end }}
						}, 
						{{- if .SMDReturn}}
							Returns: smd.JSONSchema{ 
								Name: "{{.SMDReturn.Name}}",
								Description: ` + "`{{.SMDReturn.Description}}`" + `,
								Optional:    {{.SMDReturn.HasStar}},
								{{template "smdType" .SMDReturn.SMDType }}
								{{- if and (eq .SMDReturn.SMDType.Type "Object") (ne .SMDReturn.SMDType.Ref "")}}
									{{ template "properties" (index $.Structs .SMDReturn.SMDType.Ref).Properties}}
								{{- end}}
								{{- template "definitions" definitions .SMDReturn.SMDType $.Structs }}							
							}, 
						{{- end}}
						{{- if .Errors}}
							Errors: map[int]string{
								{{- range .Errors }}
									{{.Code}}: "{{.Description}}",
								{{- end }}
							},
						{{- end}}
					}, 
				{{- end }}
			},
		}
	}

	// Invoke is as generated code from zenrpc cmd
	func (s {{.Name}}{{if $isIface}}Server{{end}}) Invoke(ctx context.Context, method string, params json.RawMessage) zenrpc.Response {
		resp := zenrpc.Response{}
		{{ if .HasErrorVariable }}var err error{{ end }}

		switch method { 
		{{- range .Methods }}
			case RPC.{{$s.Name}}.{{.Name}}: {{ if .Args }}
					var args = struct {
						{{ range .Args }}
							{{.CapitalName}} {{if and (not .HasStar) .HasDefaultValue}}*{{end}}{{.Type}} ` + "`json:\"{{.CaseName}}\"`" + `
						{{- end }}
					}{}

					if zenrpc.IsArray(params) {
						if params, err = zenrpc.ConvertToObject([]string{ 
							{{- range .Args }}"{{.CaseName}}",{{ end -}} 
							}, params); err != nil {
							return zenrpc.NewResponseError(nil, zenrpc.InvalidParams, err.Error(), nil)
						}
					}

					if len(params) > 0 {
						if err := json.Unmarshal(params, &args); err != nil {
							return zenrpc.NewResponseError(nil, zenrpc.InvalidParams, err.Error(), nil)
						}
					}

					{{ range .DefaultValues }}
						{{.Comment}}
						if args.{{.CapitalName}} == nil {
							var v {{.Type}} = {{.Value}}
							args.{{.CapitalName}} = &v
						}
					{{ end }}

				{{ end }} {{if .Returns}}
					resp.Set(s{{if $isIface}}.S{{end}}.{{.Name}}({{if .HasContext}}ctx, {{end}} {{ range .Args }}{{if and (not .HasStar) .HasDefaultValue}}*{{end}}args.{{.CapitalName}}, {{ end }}))
				{{else}}
					s{{if $isIface}}.S{{end}}.{{.Name}}({{if .HasContext}}ctx, {{end}} {{ range .Args }}{{if and (not .HasStar) .HasDefaultValue}}*{{end}}args.{{.CapitalName}}, {{ end }})
				{{end}}
		{{- end }}
		default:
			resp = zenrpc.NewResponseError(nil, zenrpc.MethodNotFound, "", nil)
		}

		return resp
	}
{{- end }}
`))
)
