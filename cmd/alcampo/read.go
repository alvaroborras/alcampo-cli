package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/alvaroborras/alcampo-cli/internal/client"
)

// freshExcludes are category-path fragments dropped by --fresh: frozen and
// canned/preserved goods, which otherwise crowd the top hits for "gambas",
// "mejillón", "atún", etc. Matched case-insensitively against every level.
var freshExcludes = []string{"congelado", "conserva"}

// isFreshExcluded reports whether a product sits under a frozen/canned category.
func isFreshExcluded(p client.Product) bool {
	for _, c := range p.CategoryPath {
		lc := strings.ToLower(c)
		for _, ex := range freshExcludes {
			if strings.Contains(lc, ex) {
				return true
			}
		}
	}
	return false
}

// searchFiltered runs one search honouring --category and --fresh. With --fresh
// it over-fetches, since the exclusion is applied client-side.
func searchFiltered(cl *client.Client, term string, limit int, categoryID string, fresh bool) (*client.SearchResult, error) {
	fetch := limit
	if fresh {
		fetch = limit * 4
	}
	res, err := cl.Search(term, fetch, categoryID)
	if err != nil {
		return nil, err
	}
	if fresh {
		kept := res.Hits[:0]
		for _, h := range res.Hits {
			if !isFreshExcluded(h) {
				kept = append(kept, h)
			}
		}
		res.Hits = kept
	}
	if len(res.Hits) > limit {
		res.Hits = res.Hits[:limit]
	}
	return res, nil
}

// resolveCategoryID returns a category UUID or OC-code directly, or resolves a
// name against the top three levels of the tree (see matchCategory).
func resolveCategoryID(cl *client.Client, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" || client.IsUUID(arg) || strings.HasPrefix(arg, "OC") {
		return arg, nil
	}
	tree, _, err := cl.Categories(3)
	if err != nil {
		return "", fmt.Errorf("resolve category %q: %w", arg, err)
	}
	return matchCategory(tree, arg)
}

// matchCategory resolves a category name case-insensitively: exact matches win
// over substrings, and among several matches the shallowest wins — the shop
// repeats names like "Congelados" as sub-aisles of promos and regional
// sections, and the top-level aisle is the one meant. A tie at the same depth is
// an error that lists the candidates.
func matchCategory(tree []client.CategoryNode, arg string) (string, error) {
	type cand struct {
		node  client.CategoryNode
		depth int
	}
	want := strings.ToLower(strings.TrimSpace(arg))
	var exact, partial []cand
	var walk func([]client.CategoryNode, int)
	walk = func(nodes []client.CategoryNode, depth int) {
		for _, n := range nodes {
			lc := strings.ToLower(n.Name)
			switch {
			case lc == want:
				exact = append(exact, cand{n, depth})
			case strings.Contains(lc, want):
				partial = append(partial, cand{n, depth})
			}
			walk(n.Children, depth+1)
		}
	}
	walk(tree, 0)
	cands := exact
	if len(cands) == 0 {
		cands = partial
	}
	var pick []client.CategoryNode
	minDepth := -1
	for _, c := range cands {
		if minDepth < 0 || c.depth < minDepth {
			minDepth = c.depth
		}
	}
	for _, c := range cands {
		if c.depth == minDepth {
			pick = append(pick, c.node)
		}
	}
	switch len(pick) {
	case 0:
		return "", fmt.Errorf("no category matches %q — run `alcampo categories` to list ids", arg)
	case 1:
		return pick[0].CategoryID, nil
	default:
		names := make([]string, len(pick))
		for i, c := range pick {
			names[i] = fmt.Sprintf("%s %s", c.RetailerCategoryID, c.Name)
		}
		return "", fmt.Errorf("category %q is ambiguous (%s) — pass an id", arg, strings.Join(names, ", "))
	}
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	cf := addCommon(fs)
	limit := fs.Int("limit", 24, "max results")
	category := fs.String("category", "", "filter to a category by id or name (see `alcampo categories`)")
	fresh := fs.Bool("fresh", false, "exclude frozen (Congelados) and canned (Conservas) results")
	_ = fs.Parse(reorderArgs(fs, args))
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: alcampo search [flags] <term...>")
	}
	cl := newClient(cf)
	catID, err := resolveCategoryID(cl, *category)
	if err != nil {
		return err
	}
	res, err := searchFiltered(cl, strings.Join(fs.Args(), " "), *limit, catID, *fresh)
	if err != nil {
		return err
	}
	if cf.jsonOut {
		return emitJSON(res)
	}
	more := ""
	if res.HasMore {
		more = "+"
	}
	fmt.Printf("query=%q  hits=%d%s\n", res.Query, len(res.Hits), more)
	for _, h := range res.Hits {
		printHit("  ", h)
	}
	return nil
}

func cmdBatch(args []string) error {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	cf := addCommon(fs)
	file := fs.String("f", "", "file with one term per line ('-' for stdin); else terms are positional args")
	hits := fs.Int("hits", 1, "results per term")
	category := fs.String("category", "", "filter every term to a category by id or name (see `alcampo categories`)")
	fresh := fs.Bool("fresh", false, "exclude frozen (Congelados) and canned (Conservas) results")
	_ = fs.Parse(reorderArgs(fs, args))
	terms, err := collectTerms(*file, fs.Args())
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return fmt.Errorf("no terms given (use -f file, stdin, or positional args)")
	}
	cl := newClient(cf)
	catID, err := resolveCategoryID(cl, *category)
	if err != nil {
		return err
	}

	// The shop has no multi-query endpoint, so terms run concurrently (bounded)
	// and are reported in input order.
	results := make([]client.SearchResult, len(terms))
	errs := make([]error, len(terms))
	var wg sync.WaitGroup
	sem := make(chan struct{}, lookupConcurrency())
	for i, t := range terms {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t string) {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := searchFiltered(cl, t, *hits, catID, *fresh)
			if err != nil {
				errs[i] = err
				results[i] = client.SearchResult{Query: t}
				return
			}
			results[i] = *r
		}(i, t)
	}
	wg.Wait()
	failed := 0
	for _, e := range errs {
		if e != nil {
			failed++
		}
	}
	if failed == len(terms) {
		return errs[0]
	}

	if cf.jsonOut {
		if err := emitJSON(results); err != nil {
			return err
		}
	} else {
		for i, r := range results {
			switch {
			case errs[i] != nil:
				fmt.Printf("• %-24s → ERROR: %v\n", terms[i], errs[i])
			case len(r.Hits) == 0:
				fmt.Printf("• %-24s → (sin resultados)\n", terms[i])
			default:
				h := r.Hits[0]
				fmt.Printf("• %-24s → [%s] %s — %s€ %s\n", terms[i], h.ID(), h.Name, h.Price.Amount, refFormat(h))
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d searches failed", failed, len(terms))
	}
	return nil
}

func cmdProduct(args []string) error {
	fs := flag.NewFlagSet("product", flag.ExitOnError)
	cf := addCommon(fs)
	_ = fs.Parse(reorderArgs(fs, args))
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: alcampo product [flags] <id>")
	}
	cl := newClient(cf)
	d, raw, err := cl.Product(fs.Arg(0))
	if err != nil {
		return err
	}
	if cf.jsonOut {
		return emitRaw(raw)
	}
	p := d.Product
	fmt.Printf("[%s] %s\n", p.ID(), p.Name)
	fmt.Printf("  precio: %s€  %s\n", p.Price.Amount, refFormat(p))
	if promo := p.Promo(); promo != "" {
		fmt.Printf("  oferta: %s\n", promo)
	}
	if p.Brand != "" {
		fmt.Printf("  marca: %s\n", p.Brand)
	}
	if p.PackSize != "" {
		fmt.Printf("  formato: %s\n", p.PackSize)
	}
	if !p.Available {
		fmt.Println("  ⚠ no disponible")
	}
	if len(p.CategoryPath) > 0 {
		fmt.Printf("  categoría: %s\n", strings.Join(p.CategoryPath, " › "))
	}
	fmt.Printf("  uuid: %s\n", p.ProductID)
	for _, line := range nutritionLines(d.Nutrition()) {
		fmt.Println(line)
	}
	return nil
}

// nutritionLines renders a product's nutrition table as indented human lines,
// or nil when the product carries none. Whole-number ".0" amounts are trimmed
// so they read as the label does ("46 Kcal"), while "1.6 g" is left intact.
func nutritionLines(n *client.Nutrition) []string {
	if n == nil || len(n.Rows) == 0 {
		return nil
	}
	head := "  nutrición"
	if n.PerQuantity != "" {
		head += " (por " + n.PerQuantity + ")"
	}
	out := []string{head + ":"}
	for _, r := range n.Rows {
		out = append(out, "    "+r.Name+": "+trimAmount(r.Value))
	}
	return out
}

// trimAmount drops a trailing ".0" from the leading number so whole numbers read
// cleanly ("193.0 Kj" → "193 Kj"); fractional amounts are returned unchanged.
func trimAmount(s string) string {
	num, unit, hasUnit := strings.Cut(s, " ")
	num = strings.TrimSuffix(num, ".0")
	if hasUnit {
		return num + " " + unit
	}
	return num
}

func cmdCategories(args []string) error {
	fs := flag.NewFlagSet("categories", flag.ExitOnError)
	cf := addCommon(fs)
	id := fs.String("id", "", "list one category's products by id (UUID or OC… code) or name")
	limit := fs.Int("limit", 50, "--id: max products")
	depth := fs.Int("depth", 2, "tree depth to show (1 = top level)")
	_ = fs.Parse(reorderArgs(fs, args))
	cl := newClient(cf)
	if *id != "" {
		catID, err := resolveCategoryID(cl, *id)
		if err != nil {
			return err
		}
		ps, raw, err := cl.CategoryProducts(catID, *limit)
		if err != nil {
			return err
		}
		if cf.jsonOut {
			return emitRaw(raw)
		}
		for _, p := range ps {
			printHit("", p)
		}
		return nil
	}
	tree, raw, err := cl.Categories(*depth)
	if err != nil {
		return err
	}
	if cf.jsonOut {
		return emitRaw(raw)
	}
	var walk func(nodes []client.CategoryNode, indent string)
	walk = func(nodes []client.CategoryNode, indent string) {
		for _, n := range nodes {
			fmt.Printf("%s%-14s %s\n", indent, n.RetailerCategoryID, n.Name)
			walk(n.Children, indent+"    ")
		}
	}
	walk(tree, "")
	return nil
}

func printHit(indent string, h client.Product) {
	extra := ""
	if cat := h.Category(); cat != "" {
		extra = " (" + cat + ")"
	}
	if promo := h.Promo(); promo != "" {
		extra += " 🏷 " + promo
	}
	if !h.Available {
		extra += " [no disponible]"
	}
	fmt.Printf("%s[%s] %s — %s€ %s%s\n", indent, h.ID(), h.Name, h.Price.Amount, refFormat(h), extra)
}

func refFormat(p client.Product) string {
	if p.UnitPrice.Price.Amount == "" || p.RefFormat() == "" {
		return ""
	}
	return fmt.Sprintf("(%s€/%s)", p.UnitPrice.Price.Amount, p.RefFormat())
}

func collectTerms(file string, posArgs []string) ([]string, error) {
	if file == "" {
		return posArgs, nil
	}
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	var terms []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" && !strings.HasPrefix(t, "#") {
			terms = append(terms, t)
		}
	}
	return terms, sc.Err()
}
