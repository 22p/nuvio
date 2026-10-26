// Package web embeds the built single-page client.
package web

import "embed"

//go:embed all:dist
var Assets embed.FS
