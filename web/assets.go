package web

import "embed"

//go:embed *.html *.css *.js *.webp
var Assets embed.FS
