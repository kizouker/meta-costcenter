// Package metacostcenter holds the files compiled into the muse-cost binary.
package metacostcenter

import "embed"

// Web is the browser dashboard, built from frontend/ by `npm run build --prefix frontend`.
//
//go:embed web/index.html web/app.js web/style.css web/logo.png
var Web embed.FS

// Pricing is the public rate snapshot used when no --prices file is configured.
//
//go:embed pricing.json
var Pricing []byte
