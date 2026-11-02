// Package expr defines the expression-language interface used for formula,
// when and reject.when. CEL is the default implementation; FEEL can replace it
// by implementing Engine.
package expr

import "time"

// Engine parses and compiles expressions.
type Engine interface {
	// Name identifies the language, e.g. "cel".
	Name() string
	// Identifiers returns the free variable names referenced by src, in order
	// of first appearance. Function names are not included.
	Identifiers(src string) ([]string, error)
	// Functions returns the custom function names src calls.
	Functions(src string) ([]string, error)
	// Compile type-checks src against the declared variables.
	Compile(src string) (Program, error)
}

// Program is a compiled expression.
type Program interface {
	// Eval evaluates the expression. vars holds row values keyed by row ID,
	// using the normalized Go types: float64, bool, string, time.Time, nil.
	Eval(vars map[string]any, ctx Context) (any, error)
}

// Context carries evaluation-scoped values that custom functions need.
type Context struct {
	// RequestTime resolves today(). It is never the real clock.
	RequestTime    time.Time
	HasRequestTime bool
}

// FunctionDoc documents one custom function.
type FunctionDoc struct {
	Signature   string
	Description string
}

// CustomFunctions documents the functions every engine must provide.
var CustomFunctions = []FunctionDoc{
	{"round(x)", "Rounds a number to the nearest whole number, halves away from zero. Returns a number."},
	{"years_between(a, b)", "Whole years from date or datetime a to b, counting a year only once its anniversary is reached. Used for ages."},
	{"today()", "The date of context.request_time, in that time's own offset. Never the real clock. A sheet that calls today() needs a row with source context.request_time."},
}
