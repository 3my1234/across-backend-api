package controllers

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func historyBackendCPU(pids []uint32) (float64, error) {
	// Linux procfs exports process ticks in USER_HZ (100), not kernel CONFIG_HZ.
	var sum float64
	for _, pid := range pids {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return 0, err
		}
		fields := strings.Fields(string(raw)[strings.LastIndex(string(raw), ")")+1:])
		for _, i := range []int{11, 12} {
			v, e := strconv.ParseFloat(fields[i], 64)
			if e != nil {
				return 0, e
			}
			sum += v / 100
		}
	}
	return sum, nil
}
