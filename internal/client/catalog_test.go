package client

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The shop ships nutrition as an HTML table; this is the shape seen live on
// product 54180 (2026-10-30).
const nutritionHTML = `<table><tbody><tr><th>Valores medios por:</th><th>100g</th></tr><tr><td><span>Valor energético (Kj)</span></td><td><span>193.0 Kj</span></td></tr><tr><td><span>Grasas</span></td><td><span>1.6 g</span></td></tr><tr><td><span>Grasas saturadas</span></td><td><span>1.0 g</span></td></tr><tr><td></td></tr></tbody></table>`

func TestParseNutritionHTML(t *testing.T) {
	n := ParseNutritionHTML(nutritionHTML)
	if n == nil {
		t.Fatal("expected a table")
	}
	want := &Nutrition{PerQuantity: "100g", Rows: []Nutrient{
		{"Valor energético (Kj)", "193.0 Kj"},
		{"Grasas", "1.6 g"},
		{"Grasas saturadas", "1.0 g"},
	}}
	if !reflect.DeepEqual(n, want) {
		t.Fatalf("got %+v, want %+v", n, want)
	}
}

func TestParseNutritionHTMLEmpty(t *testing.T) {
	for _, in := range []string{"", "  ", "<table><tr><th>Valores medios por:</th><th>100g</th></tr></table>"} {
		if n := ParseNutritionHTML(in); n != nil {
			t.Errorf("ParseNutritionHTML(%q) = %+v, want nil", in, n)
		}
	}
}

func TestProductDetailNutritionFromField(t *testing.T) {
	raw := `{"product":{"productId":"u","retailerProductId":"54180"},"bopData":{"fields":[{"title":"brand","content":"X"},{"title":"nutritionalData","content":` + mustJSON(nutritionHTML) + `}]}}`
	var d ProductDetail
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if n := d.Nutrition(); n == nil || len(n.Rows) != 3 {
		t.Fatalf("Nutrition() = %+v, want 3 rows", n)
	}
	var none ProductDetail
	if none.Nutrition() != nil {
		t.Fatal("no nutritionalData field → nil")
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestRefFormat(t *testing.T) {
	cases := map[string]string{
		"fop.price.per.litre": "l",
		"fop.price.per.kg":    "kg",
		"fop.price.per.each":  "ud",
		"":                    "",
	}
	for unit, want := range cases {
		p := Product{UnitPrice: UnitCost{Unit: unit}}
		if got := p.RefFormat(); got != want {
			t.Errorf("RefFormat(%q) = %q, want %q", unit, got, want)
		}
	}
}

func TestIDAndUUID(t *testing.T) {
	if !IsUUID("176cfd5e-afa9-4aa1-8b6f-fe3d757c535e") || IsUUID("54180") {
		t.Fatal("IsUUID misclassifies")
	}
	if (Product{ProductID: "u", RetailerProductID: "54180"}).ID() != "54180" {
		t.Fatal("ID() should prefer the retailer id")
	}
	if (Product{ProductID: "u"}).ID() != "u" {
		t.Fatal("ID() should fall back to the UUID")
	}
}

// A product can appear in several groups of one page; it must be listed once.
func TestProductPageDedupes(t *testing.T) {
	raw := `{"productGroups":[{"decoratedProducts":[{"productId":"a"},{"productId":"b"}]},{"decoratedProducts":[{"productId":"a"},{"productId":"c"}]}]}`
	var pp productPage
	if err := json.Unmarshal([]byte(raw), &pp); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range pp.products() {
		ids = append(ids, p.ProductID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("ids = %v", ids)
	}
}
