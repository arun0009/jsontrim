package jsontrim

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTrimBasic(t *testing.T) {
	raw := []byte(`{"id":"123","data":"` + strings.Repeat("x", 2000) + `"}`)
	trimmer := New(Config{FieldLimit: 500, TotalLimit: 1024, TruncateStrings: true})

	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 1024 {
		t.Errorf("Output over limit: %d > 1024", len(out))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if _, ok := m["id"]; !ok {
		t.Error("Lost 'id' field")
	}
	// Check for truncation (as TruncateStrings is true)
	if s, ok := m["data"].(string); ok && len(s) > 500 {
		t.Error("Data not truncated")
	}
}

// Tests the re-added Wildcard Blacklisting feature.
func TestWildcardBlacklist(t *testing.T) {
	raw := []byte(`{
		"users": [
			{"id": 1, "password": "abc", "info": "keep"},
			{"id": 2, "password": "xyz", "info": "keep"}
		],
		"meta": {"password": "no-match"}
	}`)

	// Pattern "users.*.password" should match users.0.password and users.1.password
	// but NOT meta.password
	trimmer := New(Config{Blacklist: []string{"users.*.password"}, TotalLimit: 2000})

	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Verification
	s := string(out)
	if strings.Contains(s, "abc") || strings.Contains(s, "xyz") {
		t.Error("Wildcard blacklist failed to remove passwords inside array")
	}
	if !strings.Contains(s, "no-match") {
		t.Error("Wildcard matched too broadly (removed meta.password)")
	}
}

// Tests the re-added ReplaceWithMarker (Ghost Markers) feature.
func TestReplaceWithMarker(t *testing.T) {
	raw := []byte(`{"keep":"me", "drop":"` + strings.Repeat("x", 200) + `"}`)
	trimmer := New(Config{
		TotalLimit:        50, // Force removal
		ReplaceWithMarker: true,
	})

	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}

	s := string(out)
	if !strings.Contains(s, Marker) {
		t.Error("Expected marker [TRIMMED] in output, got:", s)
	}
}

// Ensures order is preserved when removing an element from an array.
func TestEnforceTotalArrayOrder(t *testing.T) {
	// 100 chars, total size ~350 bytes.
	const data = "1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890"
	raw := []byte(fmt.Sprintf(`[{"d":"%s"}, {"d":"%s"}, {"d":"%s"}]`, data, data, data))

	// Strategy that targets index 1 (the middle item)
	trimmer := New(Config{
		TotalLimit: 250, // Limit is 250. Must remove at least one item.
		Strategy:   &MockStrategy{RemoveIdx: 1},
	})

	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}

	var arr []map[string]interface{}
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(arr) != 2 {
		t.Fatalf("Expected 2 items after trim, got %d. Output: %s", len(arr), out)
	}

	// Order check: Expected [item 0, item 2].
	if arr[0]["d"].(string) != data {
		t.Errorf("Order destroyed! Expected index 0 to be the first item.")
	}
	if arr[1]["d"].(string) != data {
		t.Errorf("Order destroyed! Expected index 1 to be the last item.")
	}
}

// Helper Strategy for testing specific index removal.
type MockStrategy struct {
	RemoveIdx int
}

func (m *MockStrategy) SelectNextToRemove(v interface{}) string {
	if arr, ok := v.([]interface{}); ok && len(arr) > m.RemoveIdx {
		return fmt.Sprintf("idx:%d", m.RemoveIdx)
	}
	return ""
}

func TestStrategyPrioritize(t *testing.T) {
	raw := []byte(`{"id":"1","big":"` + strings.Repeat("x", 600) + `","small":"2"}`)
	trimmer := New(Config{
		TotalLimit: 50, // Force removal
		Strategy:   PrioritizeKeys{KeepKeys: []string{"id"}},
	})

	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if _, ok := m["id"]; !ok {
		t.Error("'id' not prioritized")
	}
	if _, ok := m["big"]; ok {
		t.Error("'big' was not removed")
	}
}

func TestEnforceTotalArray(t *testing.T) {
	raw := []byte(fmt.Sprintf(`[%s, %s, %s]`, `{"d":"`+strings.Repeat("x", 300)+`"}`, `{"d":"`+strings.Repeat("x", 300)+`"}`, `{"d":"`+strings.Repeat("x", 300)+`"}`))
	trimmer := New(Config{TotalLimit: 800}) // Keep ~2 items

	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 800 {
		t.Errorf("Array over limit: %d > 800", len(out))
	}
	var arr []interface{}
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(arr) > 2 {
		t.Error("Too many array items kept")
	}
}

// Tests the defensive check for untrimmable data.
func TestErrCannotTrim(t *testing.T) {
	// Raw string is ~52 bytes. Limit is 10. TruncateStrings is false.
	raw := []byte(`"` + strings.Repeat("x", 50) + `"`)
	trimmer := New(Config{TotalLimit: 10, TruncateStrings: false})

	_, err := trimmer.Trim(raw)
	if err != ErrCannotTrim {
		t.Errorf("Expected ErrCannotTrim, got %v", err)
	}
}

func TestMaxDepth(t *testing.T) {
	// Build deep nested object
	v := make(map[string]interface{})
	current := v
	for i := 0; i < 15; i++ {
		next := make(map[string]interface{})
		current["nest"] = next
		current = next
	}
	current["leaf"] = "data"
	raw, _ := json.Marshal(v)

	trimmer := New(Config{MaxDepth: 5})
	out, err := trimmer.Trim(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Should be truncated at depth 5
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if depth := countNests(m); depth > 5 {
		t.Errorf("Exceeded max depth: %d > 5", depth)
	}
}

func countNests(m map[string]interface{}) int {
	if m == nil {
		return 0
	}
	depth := 0
	current := m
	for {
		if next, ok := current["nest"].(map[string]interface{}); ok {
			depth++
			current = next
		} else {
			break
		}
	}
	return depth
}
