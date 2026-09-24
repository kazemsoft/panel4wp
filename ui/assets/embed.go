// Package assets contains the panel's compiled, self-hosted frontend assets.
package assets

import "embed"

// Files is embedded into the Go binary so the runtime image needs no Node.js.
//
//go:embed app.css app.js htmx.min.js fonts/*.woff2
var Files embed.FS
