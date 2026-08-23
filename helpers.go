package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func jsString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}

func jsNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func truncateRunes(value string, maxChars int) string {
	if maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}

func consoleArgsText(args []struct {
	Type        string `json:"type"`
	Value       any    `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		switch {
		case arg.Value != nil:
			parts = append(parts, fmt.Sprint(arg.Value))
		case strings.TrimSpace(arg.Description) != "":
			parts = append(parts, strings.TrimSpace(arg.Description))
		case strings.TrimSpace(arg.Type) != "":
			parts = append(parts, "["+strings.TrimSpace(arg.Type)+"]")
		default:
			parts = append(parts, "[unknown]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
