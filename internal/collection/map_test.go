package collection_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"ella.to/baker/internal/collection"
)

func TestMapBasicOperations(t *testing.T) {
	t.Run("Put and Get", func(t *testing.T) {
		m := collection.NewMap[int]()

		m.Put("key1", 100)
		m.Put("key2", 200)

		val, ok := m.Get("key1")
		assert.True(t, ok)
		assert.Equal(t, 100, val)

		val, ok = m.Get("key2")
		assert.True(t, ok)
		assert.Equal(t, 200, val)
	})

	t.Run("Get non-existent key", func(t *testing.T) {
		m := collection.NewMap[string]()

		val, ok := m.Get("non-existent")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("Delete", func(t *testing.T) {
		m := collection.NewMap[int]()

		m.Put("key1", 100)
		m.Delete("key1")

		_, ok := m.Get("key1")
		assert.False(t, ok)
	})

	t.Run("Len", func(t *testing.T) {
		m := collection.NewMap[int]()

		assert.Equal(t, 0, m.Len())

		m.Put("key1", 100)
		assert.Equal(t, 1, m.Len())

		m.Put("key2", 200)
		assert.Equal(t, 2, m.Len())

		m.Delete("key1")
		assert.Equal(t, 1, m.Len())
	})

	t.Run("Overwrite existing key", func(t *testing.T) {
		m := collection.NewMap[int]()

		m.Put("key1", 100)
		m.Put("key1", 200)

		val, ok := m.Get("key1")
		assert.True(t, ok)
		assert.Equal(t, 200, val)
		assert.Equal(t, 1, m.Len())
	})
}

func TestMapGetAndUpdate(t *testing.T) {
	t.Run("Update non-existent key", func(t *testing.T) {
		m := collection.NewMap[int]()

		val := m.GetAndUpdate("key1", func(old int, found bool) int {
			assert.False(t, found)
			assert.Equal(t, 0, old)
			return 100
		})

		assert.Equal(t, 100, val)

		stored, ok := m.Get("key1")
		assert.True(t, ok)
		assert.Equal(t, 100, stored)
	})

	t.Run("Update existing key", func(t *testing.T) {
		m := collection.NewMap[int]()

		m.Put("key1", 100)

		val := m.GetAndUpdate("key1", func(old int, found bool) int {
			assert.True(t, found)
			assert.Equal(t, 100, old)
			return old + 50
		})

		assert.Equal(t, 150, val)

		stored, ok := m.Get("key1")
		assert.True(t, ok)
		assert.Equal(t, 150, stored)
	})
}

func TestMapConcurrentAccess(t *testing.T) {
	m := collection.NewMap[int]()
	var wg sync.WaitGroup
	iterations := 1000

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Put("key", i)
		}(i)
	}

	wg.Wait()

	_, ok := m.Get("key")
	assert.True(t, ok)
}

func BenchmarkMapPut(b *testing.B) {
	m := collection.NewMap[int]()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		m.Put("key", i)
	}
}

func BenchmarkMapGet(b *testing.B) {
	m := collection.NewMap[int]()
	m.Put("key", 100)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		m.Get("key")
	}
}

func BenchmarkMapGetAndUpdate(b *testing.B) {
	m := collection.NewMap[int]()
	m.Put("key", 0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		m.GetAndUpdate("key", func(old int, found bool) int {
			return old + 1
		})
	}
}
