package web

import "embed"

//go:embed all:static
var StaticFS embed.FS

//go:embed templates
var TemplateFS embed.FS
