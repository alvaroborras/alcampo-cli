package main

import (
	"reflect"
	"testing"

	"github.com/alvaroborras/alcampo-cli/internal/client"
)

// ".0" trimming is what makes the table read like the label. Pin the exact
// rendering so a formatting tweak is a deliberate, visible change.
func TestNutritionLines(t *testing.T) {
	n := &client.Nutrition{
		PerQuantity: "100g",
		Rows: []client.Nutrient{
			{Name: "Valor energético (Kcal)", Value: "46.0 Kcal"},
			{Name: "Grasas", Value: "1.6 g"},
			{Name: "Grasas saturadas", Value: "1.0 g"},
		},
	}
	want := []string{
		"  nutrición (por 100g):",
		"    Valor energético (Kcal): 46 Kcal",
		"    Grasas: 1.6 g",
		"    Grasas saturadas: 1 g",
	}
	if got := nutritionLines(n); !reflect.DeepEqual(got, want) {
		t.Fatalf("nutritionLines =\n%q\nwant\n%q", got, want)
	}
}

func TestNutritionLinesEmpty(t *testing.T) {
	if got := nutritionLines(nil); got != nil {
		t.Fatalf("nil table → %q, want nil", got)
	}
	if got := nutritionLines(&client.Nutrition{PerQuantity: "100g"}); got != nil {
		t.Fatalf("header-only table → %q, want nil (no dangling header)", got)
	}
}

func TestTrimAmount(t *testing.T) {
	cases := map[string]string{
		"193.0 Kj": "193 Kj",
		"1.6 g":    "1.6 g",
		"0.13 g":   "0.13 g",
		"10.0":     "10",
		"10.05 g":  "10.05 g",
	}
	for in, want := range cases {
		if got := trimAmount(in); got != want {
			t.Errorf("trimAmount(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsFreshExcluded(t *testing.T) {
	cases := []struct {
		path []string
		want bool
	}{
		{[]string{"Congelados", "Pescado y marisco"}, true},
		{[]string{"Alimentación", "Conservas de pescado", "Atún"}, true},
		{[]string{"Frescos", "Pescadería"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := isFreshExcluded(client.Product{CategoryPath: c.path}); got != c.want {
			t.Errorf("isFreshExcluded(%v) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestMatchCategory(t *testing.T) {
	tree := []client.CategoryNode{
		{CategoryID: "promo", Name: "Folletos y Promociones", Children: []client.CategoryNode{
			{CategoryID: "promo-cong", Name: "Congelados"},
		}},
		{CategoryID: "cong", Name: "Congelados", Children: []client.CategoryNode{
			{CategoryID: "cong-pesc", Name: "Pescado congelado"},
		}},
		{CategoryID: "fres", Name: "Frescos", Children: []client.CategoryNode{
			{CategoryID: "pesc", Name: "Pescadería"},
			{CategoryID: "carn", Name: "Carnicería"},
		}},
	}
	cases := []struct {
		arg, want string
		wantErr   bool
	}{
		{"congelados", "cong", false}, // exact at two depths → the top-level aisle
		{"PESCADERÍA", "pesc", false}, // case-insensitive exact
		{"pescado congelado", "cong-pesc", false},
		{"ería", "", true}, // two substring matches at the same depth
		{"nada", "", true},
	}
	for _, c := range cases {
		got, err := matchCategory(tree, c.arg)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("matchCategory(%q) = %q, %v; want %q, err=%v", c.arg, got, err, c.want, c.wantErr)
		}
	}
}
