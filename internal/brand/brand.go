// Package brand holds the product and command names in one place, so a rename
// touches a single file.
package brand

const (
	// Product is the name of the format and the tool.
	Product = "DerivationSheets"
	// Document is the name of one rendered sheet.
	Document = "Derivation sheet"
	// Command is the CLI binary name.
	Command = "dsheets"
	// EvidenceDir is the git-ignored folder that holds evidence runs.
	EvidenceDir = ".evidence"
)
