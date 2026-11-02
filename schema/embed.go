// Package schema embeds the JSON Schemas for sheet.toml, flow.toml and
// interface files, so the validator checks the same rules editors use.
package schema

import _ "embed"

//go:embed sheet.schema.json
var Sheet []byte

//go:embed flow.schema.json
var Flow []byte

//go:embed interface.schema.json
var Interface []byte
