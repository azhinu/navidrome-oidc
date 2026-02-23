package web

import "embed"

//go:embed *.html *.css *.js *.jpg
var Assets embed.FS
