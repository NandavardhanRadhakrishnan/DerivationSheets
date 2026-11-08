package flow

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NandavardhanRadhakrishnan/DerivationSheets/internal/sources"
)

func example(t *testing.T, mutate func(f *Flow)) *Analysis {
	t.Helper()
	f, err := Load(filepath.Join("..", "..", "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(f)
	}
	return Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))
}

func item(t *testing.T, a *Analysis, iface, field string) Item {
	t.Helper()
	for _, b := range a.Boundaries {
		if b.Iface != nil && b.Iface.Name == iface {
			for _, it := range b.Items {
				if it.Field == field {
					return it
				}
			}
		}
	}
	t.Fatalf("no item %s.%s", iface, field)
	return Item{}
}

func TestExample(t *testing.T) {
	a := example(t, nil)
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	// Layout: input, topic, request, response, output; four team steps.
	var roles []string
	for _, b := range a.Boundaries {
		roles = append(roles, b.Role)
	}
	if strings.Join(roles, ",") != "input,handoff,request,response,output" || len(a.Steps) != 4 {
		t.Fatalf("roles %v, %d steps", roles, len(a.Steps))
	}
	if a.Steps[3].KeepsFrom != 1 || a.Steps[3].Owner != "Orders team" {
		t.Errorf("continuation step = %+v", a.Steps[3])
	}
	cases := []struct {
		iface, field, status string
	}{
		{"order-placed", "shippingMethod", Missing}, // needed by the shipping call and the output
		{"order-placed", "weightKg", Warn},          // optional, but always needed
		{"order-placed", "loyaltyTier", Warn},       // enum is upper case
		{"order-placed", "country", Carried},
		{"order-placed", "orderId", Unused},
		{"quote-request", "zone", Carried}, // NONE is rejected before the call
		{"quote-request", "method", Carried},
		{"quote-response", "fee", Carried},
		{"payment-request", "currency", Missing}, // required, nothing sends it
		{"payment-request", "totalAmount", Carried},
		{"checkout-request", "order.giftMessage", Unused},
	}
	for _, c := range cases {
		if it := item(t, a, c.iface, c.field); it.Status != c.status {
			t.Errorf("%s.%s = %s (%s), want %s", c.iface, c.field, it.Status, it.Note, c.status)
		}
	}
	it := item(t, a, "order-placed", "shippingMethod")
	if !slices.Contains(it.Consumers, "Shipping fee (Shipping team)") || !strings.Contains(it.Note, "ask Orders team") {
		t.Errorf("missing item = %+v", it)
	}
	// Values the Orders team holds are kept across the call, not sent.
	if a.Transit[3]["free_shipping"] != "kept" || a.Transit[3]["shipping_fee"] != "in" {
		t.Errorf("transit at continuation = %v", a.Transit[3])
	}
	for _, b := range a.Boundaries {
		if b.Role == RoleRequest && b.Needs("free_shipping") {
			t.Error("free_shipping should not cross the call")
		}
	}
}

func TestCalleeSeesOnlyTheRequest(t *testing.T) {
	// If the Shipping team computed the discount, it would need the
	// subtotal in the quote request.
	a := example(t, func(f *Flow) {
		f.Stages[1].Computes = []string{"zone", "discount_rate", "free_shipping"}
		f.Stages[1].CalleeComputes = []string{"shipping_fee", "discount"}
	})
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	if it := item(t, a, "quote-request", "subtotal"); it.Status != Missing {
		t.Errorf("subtotal at the request = %+v", it)
	}
	if it := item(t, a, "quote-response", "discountAmount"); it.Status != Missing {
		t.Errorf("discount at the response = %+v", it)
	}
}

func TestComputedTooLate(t *testing.T) {
	a := example(t, func(f *Flow) {
		f.Stages[0].Computes = []string{"customer_years"}
		f.Output.Computes = append(f.Output.Computes, "loyalty_tier")
	})
	found := false
	for _, e := range a.Errors {
		if strings.Contains(e.Message, "Loyalty tier is computed by Orders team, but") && e.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %v", a.Errors)
	}
}

func TestStructureErrors(t *testing.T) {
	a := example(t, func(f *Flow) {
		f.Output.Producer = "Payments team" // the caller must continue after a call
		f.Stages[0].Computes = append(f.Stages[0].Computes, "no_such_row", "subtotal")
		f.Stages[1].Response.Carries["nope"] = "shipping_fee"
	})
	want := []string{"the caller (Orders team) continues", `"no_such_row"`, `"subtotal", but it is an input`, `carries "nope"`}
	for _, w := range want {
		ok := false
		for _, e := range a.Errors {
			ok = ok || strings.Contains(e.Message, w)
		}
		if !ok {
			t.Errorf("no error containing %q in %v", w, a.Errors)
		}
	}
}

func TestSheetFromSource(t *testing.T) {
	specs, err := filepath.Abs(filepath.Join("..", "..", "specs"))
	if err != nil {
		t.Fatal(err)
	}
	a := example(t, func(f *Flow) {
		f.SheetRef = "spec:order-checkout"
		f.Sources["spec"] = sources.Source{Alias: "spec", Path: specs}
	})
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	if filepath.Base(a.SheetDir) != "order-checkout" || a.Sheet == nil {
		t.Errorf("sheet loaded from %q", a.SheetDir)
	}
}

func TestSheetRefDefault(t *testing.T) {
	f, err := Load(filepath.Join("..", "..", "flows", "loyalty-report"))
	if err != nil {
		t.Fatal(err)
	}
	if f.SheetRef != "." {
		t.Errorf("SheetRef = %q", f.SheetRef)
	}
}

func TestAliases(t *testing.T) {
	a := example(t, nil)
	if got := a.Flow.Aliases(); !slices.Equal(got, []string{"shipping"}) {
		t.Errorf("Aliases() = %v", got)
	}
	a = example(t, func(f *Flow) { f.SheetRef = "spec:order-checkout"; f.Input.Interface = "web:in.toml" })
	if got := a.Flow.Aliases(); !slices.Equal(got, []string{"shipping", "spec", "web"}) {
		t.Errorf("Aliases() = %v", got)
	}
}

func TestResolverWarningsReported(t *testing.T) {
	f, err := Load(filepath.Join("..", "..", "flows", "checkout-to-payment"))
	if err != nil {
		t.Fatal(err)
	}
	r := sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir()))
	r.Warnings = append(r.Warnings, "advice from the resolver")
	a := Analyze(f, r)
	ok := false
	for _, w := range a.Warnings {
		ok = ok || w.Message == "advice from the resolver"
	}
	if !ok {
		t.Errorf("warnings = %v", a.Warnings)
	}
}

func TestExportWritesDir(t *testing.T) {
	a := example(t, nil)
	a.Flow.Sources = map[string]sources.Source{"shipping": {Alias: "shipping", Git: "https://example.com/shipping.git", Dir: "sheets"}}
	pieces, err := a.Export(filepath.Join(t.TempDir(), "pieces"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(pieces[0].Dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	want := `shipping = { git = "https://example.com/shipping.git", dir = "sheets" }`
	if !strings.Contains(string(data), want) {
		t.Errorf("exported flow.toml lacks %s:\n%s", want, data)
	}
}

// gitSource commits the shipping interfaces and the example sheet into a
// repository and returns its file:// URL.
func gitSource(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	copyTree := func(src, dst string) {
		filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			data, _ := os.ReadFile(p)
			os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755)
			return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
		})
	}
	copyTree(filepath.Join("..", "..", "interfaces", "shipping"), dir)
	copyTree(filepath.Join("..", "..", "specs", "order-checkout"), filepath.Join(dir, "order-checkout"))
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return "file://" + filepath.ToSlash(dir)
}

func TestRevisionsKeptOnFailure(t *testing.T) {
	url := gitSource(t)
	a := example(t, func(f *Flow) {
		f.Sources["shipping"] = sources.Source{Alias: "shipping", Git: url}
		f.Output.Producer = "Payments team" // an error, found after the interfaces load
	})
	if len(a.Errors) == 0 || a.Revisions["shipping"] == "" {
		t.Errorf("errors %v, revisions %v", a.Errors, a.Revisions)
	}
}

func TestSheetRevision(t *testing.T) {
	url := gitSource(t)
	a := example(t, func(f *Flow) {
		f.Sources["shipping"] = sources.Source{Alias: "shipping", Git: url}
		f.SheetRef = "shipping:order-checkout"
	})
	if len(a.Errors) > 0 || a.SheetRev == "" {
		t.Errorf("errors %v, sheet revision %q", a.Errors, a.SheetRev)
	}
}

func analyzeDir(t *testing.T, dir string) *Analysis {
	t.Helper()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := Analyze(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())))
	if len(a.Errors) > 0 {
		t.Fatalf("%s: %v", dir, a.Errors)
	}
	return a
}

func TestExportPieces(t *testing.T) {
	a := example(t, nil)
	dir := filepath.Join(t.TempDir(), "pieces")
	pieces, err := a.Export(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 3 {
		t.Fatalf("%d pieces", len(pieces))
	}
	want := []struct{ team, receives, produces string }{
		{"Storefront team", "checkout-request", "order-placed"},
		{"Orders team", "order-placed", "payment-request"},
		{"Shipping team", "quote-request", "quote-response"},
	}
	for i, w := range want {
		ex := pieces[i]
		if ex.Piece.Team != w.team || ex.Receives != w.receives || ex.Produces != w.produces {
			t.Errorf("piece %d = %s %s->%s", i+1, ex.Piece.Team, ex.Receives, ex.Produces)
		}
		// Each piece is a valid flow on its own.
		pa := analyzeDir(t, ex.Dir)
		_ = pa
	}
	if len(pieces[1].Calls) != 1 || !strings.Contains(pieces[1].Calls[0], "shipping-quote") {
		t.Errorf("orders piece calls = %v", pieces[1].Calls)
	}
	orders := analyzeDir(t, pieces[1].Dir)
	// The Orders piece reads the fee from the call and sends method to it.
	if it := item(t, orders, "quote-response", "fee"); it.Status != Carried || it.Auto {
		t.Errorf("fee = %+v", it)
	}
	if it := item(t, orders, "quote-request", "method"); it.Status != Carried || it.Auto {
		t.Errorf("method = %+v", it)
	}
	// The gap at the topic shows from both sides.
	if it := item(t, orders, "order-placed", "shippingMethod"); it.Status != Missing {
		t.Errorf("orders input shippingMethod = %+v", it)
	}
	store := analyzeDir(t, pieces[0].Dir)
	if it := item(t, store, "order-placed", "shippingMethod"); it.Status != Missing {
		t.Errorf("storefront output shippingMethod = %+v", it)
	}
}

func TestConnectBranchAndFeed(t *testing.T) {
	main := example(t, nil)
	branch := analyzeDir(t, filepath.Join("..", "..", "flows", "loyalty-report"))
	feed := analyzeDir(t, filepath.Join("..", "..", "flows", "mobile-orders"))
	Connect(branch, []*Analysis{main, feed})
	Connect(feed, []*Analysis{main})

	it := item(t, branch, "order-placed", "customerYears")
	if it.Status != Missing || !strings.Contains(it.Reason(), "probable origin: Years as customer (customer_years, computed by Storefront team") {
		t.Errorf("customerYears = %s", it.Reason())
	}
	if !strings.Contains(item(t, branch, "order-placed", "giftMessage").Reason(), "checkout-request field order.giftMessage") {
		t.Error("giftMessage not traced to the checkout request field")
	}
	if !strings.Contains(it.Reason(), "Mobile team produces order-placed in flow mobile-orders: not found upstream") {
		t.Errorf("second producer not listed: %s", it.Reason())
	}
	lt := item(t, feed, "order-placed", "loyaltyTier")
	if lt.Status != Missing || !strings.Contains(lt.Note, "flow checkout-to-payment reads it") {
		t.Errorf("feed loyaltyTier = %+v", lt)
	}
}

func analyzeWith(t *testing.T, dir string, ov Overlay) *Analysis {
	t.Helper()
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return AnalyzeWith(f, sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir())), ov)
}

// statusOf finds a row's item at the named interface, or "".
func statusOf(a *Analysis, ifaceName, row string) string {
	for _, b := range a.Boundaries {
		if b.Iface == nil || b.Iface.Name != ifaceName {
			continue
		}
		for _, it := range b.Items {
			if it.Row == row {
				return it.Status
			}
		}
	}
	return ""
}

func TestAnalyzeOverlayInterface(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "walkthrough", "3-orders-shipping")
	if got := statusOf(analyzeWith(t, dir, nil), "order-placed", "shipping_method"); got != Missing {
		t.Fatalf("without the overlay shipping_method is %q at order-placed, want missing", got)
	}
	path, err := filepath.Abs(filepath.Join("..", "..", "interfaces", "orders", "order-placed.toml"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, "\n[[field]]\nname     = \"shippingMethod\"\ntype     = \"string\"\nrequired = true\nlocal    = true\n"...)
	a := analyzeWith(t, dir, Overlay{path: data})
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	if got := statusOf(a, "order-placed", "shipping_method"); got != Carried {
		t.Errorf("with the overlay shipping_method is %q at order-placed, want carried", got)
	}
}

func TestAnalyzeNilOverlay(t *testing.T) {
	for _, dir := range []string{
		filepath.Join("..", "..", "flows", "checkout-to-payment"),
		filepath.Join("..", "..", "flows", "mobile-orders"),
		filepath.Join("..", "..", "flows", "loyalty-report"),
	} {
		f, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		r := sources.NewResolver(f.Dir, f.Sources, sources.NewFetcher(t.TempDir()))
		a, b := Analyze(f, r), AnalyzeWith(f, r, nil)
		if len(a.Errors) != len(b.Errors) || !slices.Equal(boundaryItems(a), boundaryItems(b)) {
			t.Errorf("%s: AnalyzeWith(nil) differs from Analyze", dir)
		}
	}
}

func boundaryItems(a *Analysis) []string {
	var out []string
	for _, b := range a.Boundaries {
		for _, it := range b.Items {
			out = append(out, b.Ref+" "+it.Status+" "+it.Field+" "+it.Row)
		}
	}
	return out
}

func TestParseMatchesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "flows", "checkout-to-payment")
	file := filepath.Join(dir, FileName)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(dir, file, data)
	if err != nil {
		t.Fatal(err)
	}
	if f.Slug != "checkout-to-payment" || len(f.Stages) != 2 || f.Stages[1].ID != "shipping-quote" || f.Line("stage", "1") == 0 {
		t.Errorf("parsed %+v", f)
	}
}
