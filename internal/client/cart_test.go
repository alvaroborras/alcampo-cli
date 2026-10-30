package client

import (
	"reflect"
	"testing"
)

// PlanDeltas turns the CLI's add/set semantics into the RELATIVE quantities the
// apply-quantity endpoint wants — the core of every cart write.
func TestPlanDeltas(t *testing.T) {
	current := []CartItem{{ProductID: "a", Quantity: 2}, {ProductID: "b", Quantity: 1}}
	cases := []struct {
		name    string
		changes []Change
		deltas  []QuantityDelta
		final   map[string]float64
	}{
		{"add to existing", []Change{{"a", 3, true}},
			[]QuantityDelta{{"a", 3}}, map[string]float64{"a": 5, "b": 1}},
		{"add new line", []Change{{"c", 1, true}},
			[]QuantityDelta{{"c", 1}}, map[string]float64{"a": 2, "b": 1, "c": 1}},
		{"set lower", []Change{{"a", 1, false}},
			[]QuantityDelta{{"a", -1}}, map[string]float64{"a": 1, "b": 1}},
		{"set 0 removes", []Change{{"b", 0, false}},
			[]QuantityDelta{{"b", -1}}, map[string]float64{"a": 2}},
		{"set to current is a no-op", []Change{{"a", 2, false}},
			nil, map[string]float64{"a": 2, "b": 1}},
		{"changes compose in order", []Change{{"a", 0, false}, {"a", 4, true}},
			[]QuantityDelta{{"a", 2}}, map[string]float64{"a": 4, "b": 1}},
		{"fractional", []Change{{"c", 0.5, false}},
			[]QuantityDelta{{"c", 0.5}}, map[string]float64{"a": 2, "b": 1, "c": 0.5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deltas, final := PlanDeltas(current, c.changes)
			if !reflect.DeepEqual(deltas, c.deltas) {
				t.Errorf("deltas = %v, want %v", deltas, c.deltas)
			}
			if !reflect.DeepEqual(final, c.final) {
				t.Errorf("final = %v, want %v", final, c.final)
			}
		})
	}
	if current[0].Quantity != 2 {
		t.Fatal("PlanDeltas must not mutate the input lines")
	}
}

func TestInvert(t *testing.T) {
	got := Invert([]QuantityDelta{{"a", 2}, {"b", -1}})
	want := []QuantityDelta{{"a", -2}, {"b", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Invert = %v, want %v", got, want)
	}
}

func TestCartLinesFallsBackToGroups(t *testing.T) {
	var c Cart
	c.ItemGroups = append(c.ItemGroups, struct {
		Items []CartItem `json:"items"`
	}{Items: []CartItem{{ProductID: "x", Quantity: 1}}})
	if got := c.Lines(); len(got) != 1 || got[0].ProductID != "x" {
		t.Fatalf("Lines() = %v, want the grouped item", got)
	}
}
