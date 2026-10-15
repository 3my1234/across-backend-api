package controllers

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"math"
)

type chatLocation struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Accuracy  *float64 `json:"accuracy_m,omitempty"`
}

func validCoordinates(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsNaN(longitude) && !math.IsInf(latitude, 0) && !math.IsInf(longitude, 0) && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
func validateChatLocation(pin *chatLocation) error {
	if pin == nil {
		return nil
	}
	if pin.Latitude == nil || pin.Longitude == nil || !validCoordinates(*pin.Latitude, *pin.Longitude) {
		return fiber.NewError(422, "A valid latitude and longitude are required")
	}
	if pin.Accuracy != nil && (math.IsNaN(*pin.Accuracy) || math.IsInf(*pin.Accuracy, 0) || *pin.Accuracy < 0 || *pin.Accuracy > 5000) {
		return fiber.NewError(422, "Location accuracy must be between 0 and 5000 metres")
	}
	return nil
}
func chatLocationJSON(pin *chatLocation) any {
	if pin == nil {
		return nil
	}
	encoded, _ := json.Marshal(pin)
	return string(encoded)
}
