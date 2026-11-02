// Package schema embeds the JSON Schema for sheet.toml, so the validator
// checks the same rules editors use.
package schema

import _ "embed"

//go:embed sheet.schema.json
var Sheet []byte
