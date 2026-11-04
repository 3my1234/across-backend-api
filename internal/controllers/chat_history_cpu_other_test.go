//go:build !windows && !linux

package controllers

import "errors"

func historyBackendCPU([]uint32) (float64, error) {
	return 0, errors.New("backend CPU measurement needs Windows or Linux")
}
