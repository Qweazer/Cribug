package vectordb

import "fmt"

func validateDimension(expected int, vectors [][]float64) error {
	for i, v := range vectors {
		if len(v) != expected {
			return fmt.Errorf("vector %d has dimension %d, expected %d", i, len(v), expected)
		}
	}
	return nil
}
