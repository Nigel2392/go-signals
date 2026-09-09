package omap

import (
	"reflect"
	"testing"
)

type testItem struct {
	ID    string
	Value int
}

func getTestItemKey(v testItem) string {
	return v.ID
}

func TestNewOrderedMap(t *testing.T) {
	om := NewOrderedMap(10, getTestItemKey)
	if om.Cap != 10 {
		t.Errorf("Expected capacity 10, got %d", om.Cap)
	}
	if om.Length() != 0 {
		t.Errorf("Expected initial length 0, got %d", om.Length())
	}
	if om.entries == nil || om.deleted == nil || om.index == nil {
		t.Errorf("Internal slices and maps were not correctly initialized")
	}
}

func TestSetAndGet(t *testing.T) {
	om := NewOrderedMap(5, getTestItemKey)
	item := testItem{"1", 100}

	om.Set(item)
	if !om.Has("1") {
		t.Errorf("Key '1' should be present")
	}

	val, ok := om.Get("1")
	if !ok || val != item {
		t.Errorf("Expected to get item %v, got %v (ok: %v)", item, val, ok)
	}

	if om.Length() != 1 {
		t.Errorf("Expected length 1, got %d", om.Length())
	}
}

func TestSetK(t *testing.T) {
	om := NewOrderedMap(5, getTestItemKey)
	item := testItem{"1", 100}

	om.SetK("custom_key", item)
	if !om.Has("custom_key") {
		t.Errorf("Expected to find 'custom_key'")
	}

	val, _ := om.Get("custom_key")
	if val.Value != 100 {
		t.Errorf("Expected value 100 via custom key, got %d", val.Value)
	}
}

func TestOrderPreservation(t *testing.T) {
	om := NewOrderedMap(5, getTestItemKey)

	// Additions
	om.Set(testItem{"A", 1})
	om.Set(testItem{"B", 2})
	om.Set(testItem{"C", 3})

	expectedInitial := []testItem{{"A", 1}, {"B", 2}, {"C", 3}}
	if !reflect.DeepEqual(om.List(), expectedInitial) {
		t.Errorf("Initial order incorrect. Expected: %v, got: %v", expectedInitial, om.List())
	}

	// Deletions (indirectly triggers checkdeleted on List())
	om.Delete("B")
	expectedAfterDelete := []testItem{{"A", 1}, {"C", 3}}
	if !reflect.DeepEqual(om.List(), expectedAfterDelete) {
		t.Errorf("Order after deletion incorrect. Expected: %v, got: %v", expectedAfterDelete, om.List())
	}

	// Re-additions (updates existing key, position must be retained)
	om.Set(testItem{"A", 99})
	expectedAfterUpdate := []testItem{{"A", 99}, {"C", 3}}
	if !reflect.DeepEqual(om.List(), expectedAfterUpdate) {
		t.Errorf("Order after update incorrect. Expected: %v, got: %v", expectedAfterUpdate, om.List())
	}

	// New addition (must be appended at the end)
	om.Set(testItem{"D", 4})
	expectedEnd := []testItem{{"A", 99}, {"C", 3}, {"D", 4}}
	if !reflect.DeepEqual(om.List(), expectedEnd) {
		t.Errorf("Order after new addition incorrect. Expected: %v, got: %v", expectedEnd, om.List())
	}
}

func TestCheckDeletedDirect(t *testing.T) {
	om := NewOrderedMap(5, getTestItemKey)
	om.Set(testItem{"1", 1})
	om.Set(testItem{"2", 2})
	om.Set(testItem{"3", 3})

	om.Delete("1")
	om.Delete("3")

	// Explicit call to the internal function
	om.checkdeleted()

	if om.Length() != 1 {
		t.Errorf("Length should be 1 after cleanup, got %d", om.Length())
	}

	expected := []testItem{{"2", 2}}
	if !reflect.DeepEqual(om.entries, expected) {
		t.Errorf("Entries slice incorrectly cleaned up. Expected: %v, got: %v", expected, om.entries)
	}

	// Check if the internal index is correct after cleanup
	if idx, ok := om.index["2"]; !ok || idx != 0 {
		t.Errorf("Internal index mapping is incorrect. Expected index 0 for '2', got: %d", idx)
	}
}

func TestClear(t *testing.T) {
	om := NewOrderedMap(5, getTestItemKey)
	om.Set(testItem{"1", 1})
	om.Set(testItem{"2", 2})

	om.Clear()

	if om.Length() != 0 {
		t.Errorf("Length after Clear() should be 0, got %d", om.Length())
	}

	if len(om.List()) != 0 {
		t.Errorf("List() should be empty after Clear(), got %d elements", len(om.List()))
	}
}

func TestDeleteNonExistent(t *testing.T) {
	om := NewOrderedMap(5, getTestItemKey)
	om.Set(testItem{"1", 1})

	if om.Delete("2") != false {
		t.Errorf("Delete() for non-existent key must return false")
	}
}
