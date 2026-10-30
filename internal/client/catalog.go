package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// These endpoints are anonymous reads served by the Ocado web-product-page
// service. Products carry two ids: the short numeric retailerProductId the shop
// shows in its URLs (e.g. "54180") — the id this CLI prints and accepts — and the
// platform productId UUID that the cart API needs. Either is accepted as input.

const productAPI = "/api/webproductpagews"

// Money is the platform's {amount, currency} price shape; amount is a decimal
// string like "5.28".
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// Product is the projected product ("decorated product" in Ocado terms) shared
// by search hits, category listings, product detail and cart decoration.
type Product struct {
	ProductID         string   `json:"productId"`
	RetailerProductID string   `json:"retailerProductId"`
	Name              string   `json:"name"`
	Brand             string   `json:"brand"`
	PackSize          string   `json:"packSizeDescription"`
	Price             Money    `json:"price"`
	UnitPrice         UnitCost `json:"unitPrice"`
	Available         bool     `json:"available"`
	CategoryPath      []string `json:"categoryPath"`
	Promotions        []struct {
		Description string `json:"description"`
	} `json:"promotions,omitempty"`
}

// UnitCost is the reference price (€/kg, €/l, …). Unit is an i18n key such as
// "fop.price.per.litre"; see Product.RefFormat for the short form.
type UnitCost struct {
	Price    Money  `json:"price"`
	Unit     string `json:"unit"`
	UnitName string `json:"unitName"`
}

// ID is the short, human-facing id (retailerProductId), falling back to the UUID.
func (p Product) ID() string {
	if p.RetailerProductID != "" {
		return p.RetailerProductID
	}
	return p.ProductID
}

// Category returns the leaf category name, or "" — handy for compact output.
func (p Product) Category() string {
	if len(p.CategoryPath) == 0 {
		return ""
	}
	return p.CategoryPath[len(p.CategoryPath)-1]
}

// RefFormat maps the unit-price key to a short label: "fop.price.per.kg" → "kg".
func (p Product) RefFormat() string {
	u := p.UnitPrice.Unit
	if i := strings.LastIndex(u, ".per."); i >= 0 {
		u = u[i+len(".per."):]
	}
	switch u {
	case "litre":
		return "l"
	case "each":
		return "ud"
	}
	return u
}

// Promo joins the product's promotion descriptions ("" when none).
func (p Product) Promo() string {
	var out []string
	for _, pr := range p.Promotions {
		if pr.Description != "" {
			out = append(out, pr.Description)
		}
	}
	return strings.Join(out, "; ")
}

// IsUUID reports whether id looks like a platform productId rather than a
// retailer id.
func IsUUID(id string) bool { return reUUID.MatchString(id) }

var reUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// productPage is the shared shape of search and category listing responses.
type productPage struct {
	ProductGroups []struct {
		DecoratedProducts []Product `json:"decoratedProducts"`
	} `json:"productGroups"`
	Metadata struct {
		NextPageToken string `json:"nextPageToken"`
	} `json:"metadata"`
}

// products flattens all groups, dropping duplicates (a product can appear in a
// featured group and in the main results).
func (pp productPage) products() []Product {
	var out []Product
	seen := map[string]bool{}
	for _, g := range pp.ProductGroups {
		for _, p := range g.DecoratedProducts {
			if seen[p.ProductID] {
				continue
			}
			seen[p.ProductID] = true
			out = append(out, p)
		}
	}
	return out
}

// SearchResult is the subset of a search response we expose.
type SearchResult struct {
	Query   string    `json:"query"`
	Hits    []Product `json:"hits"`
	HasMore bool      `json:"has_more"`
}

// Search runs a full-text query, optionally scoped to a category (UUID or
// OC-prefixed retailer category id). limit caps the decorated hits returned.
func (c *Client) Search(query string, limit int, categoryID string) (*SearchResult, error) {
	if limit <= 0 {
		limit = 24
	}
	q := url.Values{}
	q.Set("q", truncateRunes(query, 50)) // the web app caps the term at 50 chars
	q.Set("maxPageSize", strconv.Itoa(limit))
	q.Set("maxProductsToDecorate", strconv.Itoa(limit))
	q.Set("tag", "web")
	setCategoryParam(q, categoryID)
	var pp productPage
	if err := c.DoJSON(http.MethodGet, c.BaseURL+productAPI+"/v6/product-pages/search?"+q.Encode(), nil, &pp); err != nil {
		return nil, err
	}
	hits := pp.products()
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return &SearchResult{Query: query, Hits: hits, HasMore: pp.Metadata.NextPageToken != ""}, nil
}

// CategoryProducts lists the first page of a category's products.
func (c *Client) CategoryProducts(categoryID string, limit int) ([]Product, json.RawMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("maxPageSize", strconv.Itoa(limit))
	q.Set("maxProductsToDecorate", strconv.Itoa(limit))
	q["tag"] = []string{"web", "category-item"}
	setCategoryParam(q, categoryID)
	var raw json.RawMessage
	if err := c.DoJSON(http.MethodGet, c.BaseURL+productAPI+"/v6/product-pages?"+q.Encode(), nil, &raw); err != nil {
		return nil, nil, err
	}
	var pp productPage
	_ = json.Unmarshal(raw, &pp)
	return pp.products(), raw, nil
}

// setCategoryParam scopes a listing to a category: UUIDs go in categoryId, the
// shop's own "OC…" codes in retailerCategoryId.
func setCategoryParam(q url.Values, categoryID string) {
	switch {
	case categoryID == "":
	case IsUUID(categoryID):
		q.Set("categoryId", categoryID)
	default:
		q.Set("retailerCategoryId", categoryID)
	}
}

// CategoryNode is one node of the category tree.
type CategoryNode struct {
	CategoryID         string         `json:"categoryId"`
	RetailerCategoryID string         `json:"retailerCategoryId"`
	Name               string         `json:"name"`
	ProductCount       int            `json:"productCount"`
	Children           []CategoryNode `json:"childCategories"`
}

// Categories returns the category tree down to depth levels (1 = top level).
func (c *Client) Categories(depth int) ([]CategoryNode, json.RawMessage, error) {
	if depth <= 0 {
		depth = 2
	}
	u := fmt.Sprintf("%s%s/v1/categories?decoration=false&categoryDepth=%d", c.BaseURL, productAPI, depth)
	var raw json.RawMessage
	if err := c.DoJSON(http.MethodGet, u, nil, &raw); err != nil {
		return nil, nil, err
	}
	var tree []CategoryNode
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, raw, fmt.Errorf("parse categories: %w", err)
	}
	return tree, raw, nil
}

// ProductDetail is the projected product page (BOP): the product plus its
// description fields and the per-100g nutrition table when the shop has one.
type ProductDetail struct {
	Product Product `json:"product"`
	BopData struct {
		DetailedDescription string `json:"detailedDescription"`
		Fields              []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"fields"`
	} `json:"bopData"`
}

// Field returns a bopData field's HTML content by title ("" if absent).
func (d *ProductDetail) Field(title string) string {
	for _, f := range d.BopData.Fields {
		if f.Title == title {
			return f.Content
		}
	}
	return ""
}

// Product fetches a product page by retailer id (or productId UUID, which is
// first mapped to its retailer id). Returns the projection plus the raw JSON.
func (c *Client) Product(id string) (*ProductDetail, json.RawMessage, error) {
	if IsUUID(id) {
		ps, err := c.Decorate([]string{id})
		if err != nil {
			return nil, nil, err
		}
		if len(ps) == 0 || ps[0].RetailerProductID == "" {
			return nil, nil, fmt.Errorf("product %s not found", id)
		}
		id = ps[0].RetailerProductID
	}
	u := c.BaseURL + productAPI + "/v5/products/bop?" + url.Values{"retailerProductId": {id}}.Encode()
	var raw json.RawMessage
	if err := c.DoJSON(http.MethodGet, u, nil, &raw); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, nil, fmt.Errorf("product %s not found", id)
		}
		return nil, nil, err
	}
	var d ProductDetail
	_ = json.Unmarshal(raw, &d) // projection is best-effort
	return &d, raw, nil
}

// Decorate looks up many products by productId UUID in one request (the call
// the web app uses to render a cart). Unknown ids are simply absent.
func (c *Client) Decorate(productIDs []string) ([]Product, error) {
	if len(productIDs) == 0 {
		return nil, nil
	}
	var out struct {
		Products []Product `json:"products"`
	}
	if err := c.DoJSON(http.MethodPut, c.BaseURL+productAPI+"/v6/products", productIDs, &out); err != nil {
		return nil, err
	}
	return out.Products, nil
}

// Nutrition is the shop's nutrition table, parsed out of the HTML it ships in
// the "nutritionalData" field. PerQuantity is the column header (e.g. "100g").
type Nutrition struct {
	PerQuantity string     `json:"per_quantity"`
	Rows        []Nutrient `json:"rows"`
}

// Nutrient is one labelled row, e.g. {"Grasas", "1.6 g"}.
type Nutrient struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Nutrition returns the product's nutrition table, or nil when the shop has
// none for it (common for non-food and many fresh items).
func (d *ProductDetail) Nutrition() *Nutrition {
	if d == nil {
		return nil
	}
	return ParseNutritionHTML(d.Field("nutritionalData"))
}

var (
	reRow  = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	reCell = regexp.MustCompile(`(?is)<t([dh])[^>]*>(.*?)</t[dh]>`)
	reTag  = regexp.MustCompile(`(?s)<[^>]*>`)
)

// ParseNutritionHTML turns the shop's two-column nutrition <table> into rows.
// A <th> row is the header ("Valores medios por:" | "100g"); rows that don't
// have both a name and a value are skipped. Returns nil if no rows remain.
func ParseNutritionHTML(s string) *Nutrition {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	n := &Nutrition{}
	for _, row := range reRow.FindAllStringSubmatch(s, -1) {
		cells := reCell.FindAllStringSubmatch(row[1], -1)
		if len(cells) < 2 {
			continue
		}
		name, val := cellText(cells[0][2]), cellText(cells[1][2])
		if cells[0][1] == "h" || cells[0][1] == "H" {
			n.PerQuantity = val
			continue
		}
		if name == "" || val == "" {
			continue
		}
		n.Rows = append(n.Rows, Nutrient{Name: name, Value: val})
	}
	if len(n.Rows) == 0 {
		return nil
	}
	return n
}

func cellText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(reTag.ReplaceAllString(s, " "))), " ")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Lookup resolves many ids (retailer ids or productId UUIDs, mixed) to products.
// UUIDs go in one batched Decorate call; retailer ids need one product-page GET
// each, run with at most conc in flight. Results and per-id errors are keyed by
// the id exactly as given.
func (c *Client) Lookup(ids []string, conc int) (map[string]Product, map[string]error) {
	found := map[string]Product{}
	errs := map[string]error{}
	var uuids, retail []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if IsUUID(id) {
			uuids = append(uuids, id)
		} else {
			retail = append(retail, id)
		}
	}
	if len(uuids) > 0 {
		ps, err := c.Decorate(uuids)
		byID := map[string]Product{}
		for _, p := range ps {
			byID[p.ProductID] = p
		}
		for _, id := range uuids {
			switch p, ok := byID[id]; {
			case ok:
				found[id] = p
			case err != nil:
				errs[id] = err
			default:
				errs[id] = fmt.Errorf("product %s not found", id)
			}
		}
	}
	if conc < 1 {
		conc = 1
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, conc)
	for _, id := range retail {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			d, _, err := c.Product(id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[id] = err
				return
			}
			found[id] = d.Product
		}(id)
	}
	wg.Wait()
	return found, errs
}
