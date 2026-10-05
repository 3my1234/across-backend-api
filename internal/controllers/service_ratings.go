package controllers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func serviceMinimumRating(raw string) (float64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	rating, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(rating) || math.IsInf(rating, 0) || rating < 0 || rating > 5 {
		return 0, fmt.Errorf("min_rating must be between 0 and 5")
	}
	return rating, nil
}

func serviceAverageRating(count, sum int) float64 {
	if count <= 0 {
		return 0
	}
	return math.Round(float64(sum)/float64(count)*10) / 10
}
