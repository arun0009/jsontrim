package jsontrim

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Config holds customization options for the Trimmer.
type Config struct {
	FieldLimit        int           // Max bytes per field/object/array (default: 500)
	TotalLimit        int           // Max total output bytes (default: 1024)
	Blacklist         []string      // Paths to exclude. Supports wildcards (e.g., "users.*.email")
	Strategy          TruncStrategy // Removal order during total enforcement (default: RemoveLargest)
	MaxDepth          int           // Recursion depth limit (default: 10)
	TruncateStrings   bool          // Truncate long strings with "..." instead of dropping (default: false)
	ReplaceWithMarker bool          // If true, replaced fields become "[TRIMMED]" instead of being deleted
	Hooks             Hooks         // Optional pre/post callbacks
}

// Hooks for extensibility.
type Hooks struct {
	PreTrim  func(v interface{}) interface{}
	PostTrim func(v interface{}, err error) interface{}
}

// TruncStrategy defines removal policies for EnforceTotalLimit.
type TruncStrategy interface {
	// SelectNextToRemove identifies the next field/item to drop.
	// Returns key (map) or "idx:N" (array). Empty string if done.
	SelectNextToRemove(v interface{}) string
}

// Built-in strategies.
type (
	RemoveLargest  struct{}
	FIFO           struct{}
	PrioritizeKeys struct {
		KeepKeys []string
		Fallback TruncStrategy
	}
)

var (
	// ErrCannotTrim indicates the JSON couldn't be reduced below limits.
	ErrCannotTrim = errors.New("cannot trim JSON below limits")
	// Marker is the value used when ReplaceWithMarker is true.
	Marker = "[TRIMMED]"
)

// SelectNextToRemove for RemoveLargest: Finds the largest by approximate size.
func (s RemoveLargest) SelectNextToRemove(v interface{}) string {
	switch vv := v.(type) {
	case map[string]interface{}:
		maxKey := ""
		maxSize := 0
		for k, val := range vv {
			// Optimization: Use size estimation to avoid heavy allocations
			sz := estimateSize(val)
			if sz > maxSize {
				maxSize = sz
				maxKey = k
			}
		}
		return maxKey
	case []interface{}:
		maxIdx := -1
		maxSize := 0
		for i, item := range vv {
			sz := estimateSize(item)
			if sz > maxSize {
				maxSize = sz
				maxIdx = i
			}
		}
		if maxIdx >= 0 {
			return fmt.Sprintf("idx:%d", maxIdx)
		}
	}
	return ""
}

// SelectNextToRemove for FIFO: First key or index 0.
func (s FIFO) SelectNextToRemove(v interface{}) string {
	switch vv := v.(type) {
	case map[string]interface{}:
		for k := range vv {
			return k
		}
	case []interface{}:
		if len(vv) > 0 {
			return fmt.Sprintf("idx:%d", 0)
		}
	}
	return ""
}

// SelectNextToRemove for PrioritizeKeys: Skips keep keys, falls back.
func (s PrioritizeKeys) SelectNextToRemove(v interface{}) string {
	fallback := s.Fallback
	if fallback == nil {
		fallback = FIFO{}
	}

	if len(s.KeepKeys) == 0 {
		return fallback.SelectNextToRemove(v)
	}

	switch vv := v.(type) {
	case map[string]interface{}:
		// Use fallback on a subset of candidates to preserve order
		candidates := make(map[string]interface{})
		for k, val := range vv {
			isKeep := false
			for _, kk := range s.KeepKeys {
				if k == kk {
					isKeep = true
					break
				}
			}
			if !isKeep {
				candidates[k] = val
			}
		}
		if len(candidates) > 0 {
			return fallback.SelectNextToRemove(candidates)
		}
		// If only keep-keys remain, we fall back to trimming them if needed
		return fallback.SelectNextToRemove(v)
	}
	return ""
}

// Trimmer is the main struct.
type Trimmer struct {
	cfg            Config
	blacklistParts [][]string // Pre-split paths for faster wildcard matching
}

// New creates a Trimmer with defaults filled.
func New(cfg Config) *Trimmer {
	if cfg.FieldLimit == 0 {
		cfg.FieldLimit = 500
	}
	if cfg.TotalLimit == 0 {
		cfg.TotalLimit = 1024
	}
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 10
	}
	if cfg.Strategy == nil {
		cfg.Strategy = RemoveLargest{}
	}
	if cfg.Hooks.PreTrim == nil {
		cfg.Hooks.PreTrim = func(v interface{}) interface{} { return v }
	}
	if cfg.Hooks.PostTrim == nil {
		cfg.Hooks.PostTrim = func(v interface{}, err error) interface{} { return v }
	}

	t := &Trimmer{cfg: cfg}
	// Pre-process blacklist for wildcard support (Feature re-added)
	for _, p := range cfg.Blacklist {
		t.blacklistParts = append(t.blacklistParts, strings.Split(p, "."))
	}
	return t
}

// Trim takes raw JSON bytes, strips blacklist, applies limits, and returns trimmed bytes.
func (t *Trimmer) Trim(raw []byte) ([]byte, error) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}

	// Step 0: Strip blacklisted paths (Wildcard aware)
	v = t.stripBlacklisted(v)

	// Hooks: Pre
	v = t.cfg.Hooks.PreTrim(v)

	// Step 1: Trim oversized fields (recursive)
	v = t.trimFields(v, 1)

	// Step 2: Enforce total limit
	v = t.enforceTotal(v)

	// Hooks: Post
	v = t.cfg.Hooks.PostTrim(v, nil)

	out, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Defensive check
	if len(out) > t.cfg.TotalLimit {
		return nil, ErrCannotTrim
	}

	return out, nil
}

// stripBlacklisted removes fields matching the config paths (Wildcard Feature re-added).
func (t *Trimmer) stripBlacklisted(v interface{}) interface{} {
	if len(t.blacklistParts) == 0 {
		return v
	}
	return t.stripRecursive(v, []string{})
}

func (t *Trimmer) stripRecursive(v interface{}, currentPath []string) interface{} {
	// Check if current path matches any blacklist rule
	if t.matchesBlacklist(currentPath) {
		if t.cfg.ReplaceWithMarker {
			return Marker
		}
		return nil
	}

	switch vv := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{})
		for k, val := range vv {
			newPath := append(currentPath, k)
			stripped := t.stripRecursive(val, newPath)
			if stripped != nil {
				out[k] = stripped
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(vv))
		for i, item := range vv {
			// Arrays use index in path for matching, e.g., "data.0"
			newPath := append(currentPath, fmt.Sprintf("%d", i))
			stripped := t.stripRecursive(item, newPath)
			if stripped != nil {
				out = append(out, stripped)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return v
}

// matchesBlacklist checks if the current path slice matches any blacklist pattern (Wildcard Feature re-added).
func (t *Trimmer) matchesBlacklist(path []string) bool {
	if len(path) == 0 {
		return false
	}
	for _, rule := range t.blacklistParts {
		if len(rule) != len(path) {
			continue
		}
		match := true
		for i, part := range rule {
			// Wildcard match or exact match
			if part != "*" && part != path[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// trimFields recursively trims nested content (Marker Feature re-added).
func (t *Trimmer) trimFields(v interface{}, depth int) interface{} {
	if depth > t.cfg.MaxDepth {
		if t.cfg.ReplaceWithMarker {
			return Marker
		}
		return nil
	}

	switch vv := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{})
		for k, val := range vv {
			trimmed := t.trimFields(val, depth+1)
			if trimmed == nil {
				continue
			}
			// Check individual field size
			if estimateSize(trimmed) > t.cfg.FieldLimit { // Use estimateSize
				// Verify with precise marshal
				encoded, _ := json.Marshal(trimmed)
				if len(encoded) > t.cfg.FieldLimit {
					if t.cfg.ReplaceWithMarker {
						out[k] = Marker
					}
					continue
				}
			}
			out[k] = trimmed
		}
		return out

	case []interface{}:
		out := make([]interface{}, 0, len(vv))
		for _, item := range vv {
			trimmed := t.trimFields(item, depth+1)
			if trimmed == nil {
				continue
			}
			if estimateSize(trimmed) > t.cfg.FieldLimit { // Use estimateSize
				encoded, _ := json.Marshal(trimmed)
				if len(encoded) > t.cfg.FieldLimit {
					if t.cfg.ReplaceWithMarker {
						out = append(out, Marker)
					}
					continue
				}
			}
			out = append(out, trimmed)
		}
		return out
	}

	// Primitives
	if str, ok := v.(string); ok {
		if len(str) > t.cfg.FieldLimit {
			if t.cfg.TruncateStrings {
				newLen := t.cfg.FieldLimit - 6
				if newLen > 0 && len(str) > newLen {
					return str[:newLen] + "..."
				}
			}
			if t.cfg.ReplaceWithMarker {
				return Marker
			}
			return nil
		}
	}

	return v
}

// enforceTotal iteratively applies strategy until under limit.
func (t *Trimmer) enforceTotal(v interface{}) interface{} {
	for {
		encoded, err := json.Marshal(v)
		if err != nil {
			return v
		}
		if len(encoded) <= t.cfg.TotalLimit {
			return v
		}

		toRemove := t.cfg.Strategy.SelectNextToRemove(v)
		if toRemove == "" {
			break
		}

		switch vv := v.(type) {
		case map[string]interface{}:
			if !strings.HasPrefix(toRemove, "idx:") {
				if t.cfg.ReplaceWithMarker && vv[toRemove] != Marker {
					vv[toRemove] = Marker
				} else {
					delete(vv, toRemove)
				}
			}
		case []interface{}:
			if strings.HasPrefix(toRemove, "idx:") {
				var idx int
				if _, err := fmt.Sscanf(toRemove[4:], "%d", &idx); err == nil && idx >= 0 && idx < len(vv) {
					// Use ReplaceWithMarker if enabled and not already a marker
					if t.cfg.ReplaceWithMarker && vv[idx] != Marker {
						vv[idx] = Marker
					} else {
						copy(vv[idx:], vv[idx+1:]) // Shift elements left
						vv = vv[:len(vv)-1]        // Slice off the last element
						v = vv                     // Update reference
					}
				}
			}
		}
	}
	return v
}

// estimateSize provides a rough byte count to avoid expensive Marshaling (Performance Feature re-added).
func estimateSize(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case string:
		return len(val) + 2 // + quotes
	case bool:
		if val {
			return 4
		}
		return 5
	case float64:
		return 8 // Very rough
	case map[string]interface{}:
		s := 2 // {}
		for k, sub := range val {
			s += len(k) + 2 + 1 // "key":
			s += estimateSize(sub)
			s += 1 // comma
		}
		return s
	case []interface{}:
		s := 2 // []
		for _, sub := range val {
			s += estimateSize(sub)
			s += 1
		}
		return s
	default:
		// Fallback for complex types using reflection, rarely hit in unmarshaled JSON
		return int(reflect.TypeOf(val).Size())
	}
}
