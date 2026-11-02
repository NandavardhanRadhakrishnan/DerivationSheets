package expr

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// CEL is the Common Expression Language engine.
//
// Sheet values are loosely typed, so every row is declared as dyn. Sheets have
// a single number type, so every number is a CEL double: integer literals in
// an expression are read as doubles (`x * 2` means `x * 2.0`), and the custom
// functions return doubles. Without this, CEL's strict numeric rules would
// reject `base_premium * 2`. Comparisons across int and double are also
// enabled, for values such as size() that CEL itself returns as int.
type CEL struct {
	env   *cel.Env
	mu    sync.Mutex
	cache map[string]*celProgram
}

// NewCEL declares each name in vars as a dyn variable.
func NewCEL(vars []string) (*CEL, error) {
	opts := []cel.EnvOption{
		cel.CrossTypeNumericComparisons(true),
		cel.DefaultUTCTimeZone(true),
	}
	for _, v := range vars {
		opts = append(opts, cel.Variable(v, cel.DynType))
	}
	opts = append(opts, customFunctions()...)
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	return &CEL{env: env, cache: map[string]*celProgram{}}, nil
}

func (c *CEL) Name() string { return "cel" }

func (c *CEL) parse(src string) (*ast.AST, error) {
	parsed, iss := c.env.Parse(src)
	if iss != nil && iss.Err() != nil {
		return nil, cleanErr(iss.Err())
	}
	return parsed.NativeRep(), nil
}

func (c *CEL) Identifiers(src string) ([]string, error) {
	a, err := c.parse(src)
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	var idents []string
	seen := map[string]bool{}
	ast.PreOrderVisit(ast.NavigateAST(a), ast.NewExprVisitor(func(e ast.Expr) {
		switch e.Kind() {
		case ast.ComprehensionKind:
			comp := e.AsComprehension()
			bound[comp.IterVar()] = true
			if v := comp.IterVar2(); v != "" {
				bound[v] = true
			}
			bound[comp.AccuVar()] = true
		case ast.IdentKind:
			name := e.AsIdent()
			if !seen[name] {
				seen[name] = true
				idents = append(idents, name)
			}
		}
	}))
	out := idents[:0]
	for _, id := range idents {
		if !bound[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (c *CEL) Functions(src string) ([]string, error) {
	a, err := c.parse(src)
	if err != nil {
		return nil, err
	}
	var fns []string
	seen := map[string]bool{}
	ast.PreOrderVisit(ast.NavigateAST(a), ast.NewExprVisitor(func(e ast.Expr) {
		if e.Kind() == ast.CallKind {
			fn := e.AsCall().FunctionName()
			if !seen[fn] {
				seen[fn] = true
				fns = append(fns, fn)
			}
		}
	}))
	return fns, nil
}

func (c *CEL) Compile(src string) (Program, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.cache[src]; ok {
		return p, nil
	}
	checked, iss := c.env.Compile(DoubleLiterals(src))
	if iss != nil && iss.Err() != nil {
		return nil, cleanErr(iss.Err())
	}
	prg, err := c.env.Program(checked)
	if err != nil {
		return nil, err
	}
	p := &celProgram{prg: prg}
	c.cache[src] = p
	return p, nil
}

type celProgram struct{ prg cel.Program }

func (p *celProgram) Eval(vars map[string]any, ctx Context) (any, error) {
	act, err := cel.FunctionVars(vars, map[string]cel.LateBoundFunction{
		"today": func(string, ...ref.Val) ref.Val {
			if !ctx.HasRequestTime {
				return types.NewErr("today() needs a row with source context.request_time")
			}
			t := ctx.RequestTime
			return types.Timestamp{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())}
		},
	})
	if err != nil {
		return nil, err
	}
	out, _, err := p.prg.Eval(act)
	if err != nil {
		return nil, err
	}
	return FromCEL(out), nil
}

// FromCEL converts a CEL value to the normalized Go types used by sheets.
// All numbers become float64.
func FromCEL(v ref.Val) any {
	switch x := v.(type) {
	case types.Double:
		return float64(x)
	case types.Int:
		return float64(x)
	case types.Uint:
		return float64(x)
	case types.Bool:
		return bool(x)
	case types.String:
		return string(x)
	case types.Timestamp:
		return x.Time
	case types.Null:
		return nil
	}
	return v.Value()
}

func cleanErr(err error) error {
	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "ERROR: <input>:")
	return fmt.Errorf("%s", strings.ReplaceAll(msg, "\n", " "))
}

func customFunctions() []cel.EnvOption {
	toTime := func(v ref.Val) (time.Time, bool) {
		if ts, ok := v.(types.Timestamp); ok {
			return ts.Time, true
		}
		return time.Time{}, false
	}
	return []cel.EnvOption{
		cel.Function("round",
			cel.Overload("round_double", []*cel.Type{cel.DoubleType}, cel.DoubleType,
				cel.UnaryBinding(func(v ref.Val) ref.Val { return types.Double(math.Round(float64(v.(types.Double)))) })),
		),
		cel.Function("years_between",
			cel.Overload("years_between_timestamp_timestamp", []*cel.Type{cel.TimestampType, cel.TimestampType}, cel.DoubleType,
				cel.BinaryBinding(func(a, b ref.Val) ref.Val {
					from, ok1 := toTime(a)
					to, ok2 := toTime(b)
					if !ok1 || !ok2 {
						return types.NoSuchOverloadErr()
					}
					return types.Double(YearsBetween(from, to))
				})),
		),
		cel.Function("today",
			cel.Overload("today_timestamp", []*cel.Type{}, cel.TimestampType, cel.LateFunctionBinding()),
		),
	}
}

// YearsBetween counts whole years from a to b, by calendar date. Each date is
// read in its own offset, so a date of birth stored at UTC midnight compares
// correctly with a request time in +05:30.
func YearsBetween(a, b time.Time) int64 {
	if b.Before(a) {
		return -YearsBetween(b, a)
	}
	years := int64(b.Year() - a.Year())
	if b.Month() < a.Month() || (b.Month() == a.Month() && b.Day() < a.Day()) {
		years--
	}
	return years
}

// DoubleLiterals rewrites integer literals to double literals, skipping string
// literals, identifiers, hex and unsigned literals, and numbers that already
// have a fraction or exponent.
func DoubleLiterals(src string) string {
	var b strings.Builder
	isIdent := func(c byte) bool {
		return c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(src) {
				j++
			}
			b.WriteString(src[i:j])
			i = j
		case c >= '0' && c <= '9' && (i == 0 || !isIdent(src[i-1])):
			j := i
			for j < len(src) && isIdent(src[j]) {
				j++
			}
			tok := src[i:j]
			b.WriteString(tok)
			if !strings.ContainsAny(tok, ".eExXuU") {
				b.WriteString(".0")
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
