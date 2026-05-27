package main

import (
	"encoding/json"
	"fmt"
	"math"
)

func main() {
	P := 1000.0
	r := 0.05
	t := 10.0

	simple := P * (1 + r*t)
	compound := P * math.Pow(1+r, t)
	diff := compound - simple

	result := map[string]interface{}{
		"principal":                   P,
		"rate":                        r,
		"years":                       t,
		"simple_interest_formula":     "A = P * (1 + r * t)",
		"compound_interest_formula":   "A = P * (1 + r)^t",
		"simple_interest_amount":      math.Round(simple*100) / 100,
		"compound_interest_amount":    math.Round(compound*100) / 100,
		"difference":                  math.Round(diff*100) / 100,
	}

	out, _ := json.Marshal(result)
	fmt.Print(string(out))
}
