package trie_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"ella.to/baker/internal/trie"
)

func TestPaths(t *testing.T) {

	t.Run("testing adding new value to trie", func(t *testing.T) {
		trie := trie.New[int]()
		trie.Put([]rune("/"), 1)

		assert.Equal(t, 1, trie.Size())
		assert.Equal(t, 1, trie.Get([]rune("/")))

		trie.Del([]rune("/"))
		assert.Equal(t, 0, trie.Size())
		assert.Equal(t, 0, trie.Get([]rune("/")))
	})

	t.Run("testing children", func(t *testing.T) {
		trie := trie.New[int]()
		trie.Put([]rune("/a/b/c"), 1)
		trie.Put([]rune("/a/b"), 2)

		// assert.Equal(t, 2, trie.Size())

		assert.Equal(t, 1, trie.Get([]rune("/a/b/c")))
		assert.Equal(t, 2, trie.Get([]rune("/a/b")))

		trie.Del([]rune("/a/b/c"))
		// assert.Equal(t, 1, trie.Size())
		assert.Equal(t, 2, trie.Get([]rune("/a/b")))
		assert.Equal(t, 0, trie.Get([]rune("/a/b/c")))
	})
}

func BenchmarkPut(b *testing.B) {
	trie := trie.New[int]()
	path := []rune("/a/b/c")
	for i := 0; i < b.N; i++ {
		trie.Put(path, i)
	}
}

func BenchmarkPutALot(b *testing.B) {
	trie := trie.New[int]()

	paths := make([][]rune, 1000)
	for i := 0; i < 1000; i++ {
		paths[i] = generateRandomPath()

	}

	for i := 0; i < b.N; i++ {
		trie.Put(paths[i%1000], i)
	}
}

func BenchmarkGet(b *testing.B) {
	trie := trie.New[int]()
	path := []rune("/a/b/c")
	trie.Put(path, 1)
	for i := 0; i < b.N; i++ {
		trie.Get(path)
	}
}

func BenchmarkDel(b *testing.B) {
	trie := trie.New[int]()
	path := []rune("/a/b/c")

	for i := 0; i < b.N; i++ {
		trie.Put(path, i)
		trie.Del(path)
	}
}

func generateRandomPath() []rune {
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	pathLength := seed.Intn(10) + 1 // Generate a random path length between 1 and 10
	path := make([]rune, pathLength)
	for i := 0; i < pathLength; i++ {
		path[i] = rune(rand.Intn(26) + 97) // Generate a random lowercase letter
	}
	return path
}
